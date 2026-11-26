#!/usr/bin/env python3
"""End-to-end smoke suite: real agent binary + QA fixtures, asserted.

Runs a representative slice of TEST_CASES.md against a freshly built runtime
and exits non-zero on any failure. Used by CI; also handy locally:

    python3 qa/ci_smoke.py
"""
import copy
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
FIX = os.path.join(ROOT, "qa", "fixtures")
WORK = tempfile.mkdtemp(prefix="oh-smoke-")
BIN = os.path.join(WORK, "harness")
RESULTS = []


def spec(name, **overrides):
    with open(os.path.join(FIX, "specs", name + ".json")) as f:
        s = json.load(f)
    s = copy.deepcopy(s)
    for path, val in overrides.items():
        cur = s
        parts = path.split("__")
        for p in parts[:-1]:
            cur = cur[p]
        cur[parts[-1]] = val
    # Keep state inside the temp dir.
    s["memory"]["path"] = os.path.join(WORK, s["name"], "memory.json")
    s["sessions"]["dir"] = os.path.join(WORK, s["name"], "sessions")
    s["permissions"]["workspaceRoot"] = os.path.join(WORK, s["name"], "workspace")
    s["observability"]["traceFile"] = os.path.join(WORK, s["name"], "trace.jsonl")
    return s


def wait(url, seconds=20):
    deadline = time.time() + seconds
    while time.time() < deadline:
        try:
            urllib.request.urlopen(url, timeout=1)
            return True
        except Exception:
            time.sleep(0.2)
    return False


class Agent:
    def __init__(self, s, port):
        s = copy.deepcopy(s)
        s["server"]["port"] = port
        self.port = port
        path = os.path.join(WORK, "spec-%d.json" % port)
        with open(path, "w") as f:
            json.dump(s, f)
        self.log = open(os.path.join(WORK, "agent-%d.log" % port), "w")
        env = dict(os.environ, QA_TOOL_TOKEN="tok123", QA_HARNESS_KEYS="k1,k2")
        self.proc = subprocess.Popen([BIN, "--spec", path], cwd=ROOT, stdout=self.log, stderr=self.log, env=env)
        if not wait("http://127.0.0.1:%d/healthz" % port):
            self.stop()
            raise RuntimeError("agent on :%d did not start; see %s" % (port, self.log.name))

    def send(self, text, stream=False, session=None, history=None, headers=None, system=None):
        msgs = ([{"role": "system", "content": system}] if system else []) + (history or []) + [{"role": "user", "content": text}]
        body = {"model": "qa", "messages": msgs, "stream": stream}
        h = {"Content-Type": "application/json"}
        if session:
            h["X-Session-Id"] = session
        h.update(headers or {})
        req = urllib.request.Request("http://127.0.0.1:%d/v1/chat/completions" % self.port, json.dumps(body).encode(), h)
        try:
            r = urllib.request.urlopen(req, timeout=60)
        except urllib.error.HTTPError as e:
            return {"status": e.code, "content": e.read().decode(), "activity": "", "finish": None}
        if not stream:
            j = json.load(r)
            ch = j["choices"][0]
            return {"status": 200, "content": ch["message"].get("content") or "", "activity": "", "finish": ch["finish_reason"]}
        content, activity, finish = [], [], None
        for line in r:
            line = line.decode().strip()
            if line.startswith("data: {"):
                for ch in json.loads(line[6:]).get("choices", []):
                    content.append(ch["delta"].get("content") or "")
                    activity.append(ch["delta"].get("reasoning_content") or "")
                    finish = ch.get("finish_reason") or finish
        return {"status": 200, "content": "".join(content), "activity": "".join(activity), "finish": finish}

    def stop(self):
        self.proc.terminate()
        try:
            self.proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            self.proc.kill()
        self.log.close()


def check(case_id, ok, detail):
    RESULTS.append((case_id, ok, detail))
    print("%s %-8s %s" % ("PASS" if ok else "FAIL", case_id, detail[:150].replace("\n", " ")), flush=True)


def has(r, *needles, absent=()):
    text = r["content"] + "\n" + r["activity"]
    return all(n in text for n in needles) and not any(n in text for n in absent)


def main():
    subprocess.check_call(["go", "build", "-o", BIN, "./cmd/harness"], cwd=ROOT)
    fixtures = [subprocess.Popen([sys.executable, os.path.join(FIX, "mock_llm.py")], stdout=subprocess.DEVNULL),
                subprocess.Popen([sys.executable, os.path.join(FIX, "webhook_server.py")], stdout=subprocess.DEVNULL)]
    agents = []
    try:
        assert wait("http://127.0.0.1:18098/stats") and wait("http://127.0.0.1:18099/log"), "fixtures did not start"

        ks = Agent(spec("qa-kitchen-sink"), 8090)
        agents.append(ks)
        r = ks.send('[[tool:calculator {"expression":"6*7"}]]', stream=True, session="c1")
        check("LOOP-01", has(r, "TOOL_RESULT: 42", "▸ calculator"), r["content"])
        r = ks.send("[[echo-system]]", session="p1")
        check("PRM-02", has(r, "## Style", "## Skills", "QA-STATIC-NOTE", absent=("THIS MUST NOT APPEAR", "SKILL-REFUNDS-BODY")), r["content"])
        r = ks.send('[[tool:qa_lookup {"id":"A 1","verbose":true}]]', session="h1")
        check("TLH-01", has(r, '"id": "A 1"', '"verbose": "true"', "tok123"), r["content"])
        r = ks.send('[[tool:qa_create {"title":"hi"}]]', session="h2")
        check("TLH-02", has(r, '"method": "POST"', '"title": "hi"', "Bearer tok123"), r["content"])
        r = ks.send('[[tool:mcp_qastdio_env {"name":"MCP_QA_VAR"}]]', session="m1")
        check("TLM-03", has(r, "hello-from-spec"), r["content"])
        r = ks.send('[[tool:mcp_qahttp_add {"a":2,"b":3}]]', session="m2")
        check("TLM-05", has(r, "TOOL_RESULT: 5"), r["content"])
        r = ks.send('[[tool:mcp_qastdio_fail {}]]', session="m3")
        check("TLM-04", has(r, "deliberate MCP failure"), r["content"])
        r = ks.send('[[tool:load_skill {"name":"refunds"}]]', session="s1")
        check("SKL-01", has(r, "SKILL-REFUNDS-BODY"), r["content"])
        r = ks.send("[[subagent:mathwhiz]]", stream=True, session="sa1")
        check("SUB-01", has(r, "TOOL_RESULT: TOOL_RESULT: 256", "│ ▸ calculator"), r["content"])
        r = ks.send("[[subagent:mathwhiz current_time]]", session="sa2")
        check("SUB-02", has(r, 'unknown tool \\"current_time\\"') or has(r, 'unknown tool "current_time"'), r["content"])
        r = ks.send('[[tool:http_fetch {"url":"http://127.0.0.1:18099/page"}]]', session="f1")
        check("TLB-04", has(r, "4242", "alpha", absent=("should-not-appear",)), r["content"])
        r = ks.send('[[tool:http_fetch {"url":"http://localhost:18099/page"}]]', session="f2")
        check("TLB-05", has(r, "not in the allowlist"), r["content"])
        r = ks.send('[[tool:read_file {"path":"../../etc/hosts"}]]', session="w1")
        check("TLB-11", has(r, "outside the workspace"), r["content"])
        r = ks.send('[[tool:shell {"command":"rm","args":["-rf","x"]}]]', session="sh1")
        check("TLB-12", has(r, "not in the allowlist"), r["content"])
        r = ks.send('[[tool:shell {"command":"echo","args":["$HOME"]}]]', session="sh2")
        check("TLB-13", has(r, "$HOME"), r["content"])
        r = ks.send("the password is SwordFish", session="g1")
        check("GRD-01", r["finish"] == "content_filter" and "QA: request blocked." in r["content"], str(r))
        r = ks.send("[[say:your id is ID-1234 ok]]", session="g2")
        check("GRD-04", r["content"] == "your id is [REDACTED] ok", r["content"])
        r = ks.send('[[tool:memory_save {"text":"likes tea"}]]', session="mem")
        r = ks.send("[[echo-system]]", session="mem")
        check("MEM-03", has(r, "## What you remember", "likes tea"), r["content"])
        r = ks.send("[[echo-system]]", session="other")
        check("MEM-04", not has(r, "likes tea"), r["content"])
        ks.send("one", session="sess")
        r = ks.send("[[echo-messages]]", session="sess")
        check("SES-02", r["content"].startswith("MESSAGES 4"), r["content"])
        ks.send("continue", session="rep")
        ks.send("continue", session="rep")
        last = json.load(urllib.request.urlopen("http://127.0.0.1:18098/requests"))[-1]["body"]["messages"]
        check("SES-04", len(last) == 4 and last[-1]["role"] == "user", "%d messages" % len(last))
        r = ks.send("[[loop]]", session="loop")
        check("LOOP-05", has(r, "FINAL AFTER STEP LIMIT"), r["content"])
        ks.stop()
        agents.remove(ks)

        cases = [
            ("MOD-02", spec("qa-anthropic"), dict(text='[[tool:calculator {"expression":"2+2"}]]', stream=True), lambda r: has(r, "TOOL_RESULT: 4")),
            ("LOOP-02", spec("qa-base", loop__strategy="plan_execute"), dict(text="hello", stream=True), lambda r: has(r, "Plan:", "1. Step one")),
            ("LOOP-10", spec("qa-base", loop__onToolError="stop"), dict(text='[[tool:calculator {"expression":"1/0"}]]'), lambda r: has(r, "I stopped because a tool failed")),
            ("LOOP-12", spec("qa-base", loop__selfCheck={"enabled": True, "maxRevisions": 2, "prompt": "Review your draft answer. If it is complete and correct reply with exactly APPROVED."}),
             dict(text="[[selfcheck:revise]] draft", stream=True), lambda r: r["content"] == "REVISED ANSWER" and r["activity"].count("revised") == 2),
            ("CTX-02", spec("qa-base", context__strategy="summarize", context__maxContextTokens=60, context__keepRecentMessages=2),
             dict(text="[[echo-messages]]", stream=True, history=[{"role": "user", "content": "word " * 60}, {"role": "assistant", "content": "ok"}] * 4),
             lambda r: has(r, "Compacted context (summarize)") and r["content"].startswith("MESSAGES 4")),
            ("HOOK-05", spec("qa-base", hooks=[{"event": "pre_tool", "type": "webhook", "url": "http://127.0.0.1:18099/hook?action=block&message=nope", "blocking": True, "timeoutMs": 2000}]),
             dict(text='[[tool:calculator {"expression":"1+1"}]]'), lambda r: has(r, "blocked by hook: nope")),
            ("HOOK-06", spec("qa-base", prompt__clientSystemPolicy="ignore", hooks=[{"event": "turn_start", "type": "inject", "content": "HOOK-CTX", "blocking": True}]),
             dict(text="[[echo-system]]", system="CLIENT-SYS"), lambda r: has(r, "## Context from hooks", "HOOK-CTX", absent=("CLIENT-SYS",))),
            ("PERM-04", spec("qa-base", permissions__toolPolicies={"calculator": "webhook"}, permissions__approvalWebhook="http://127.0.0.1:18099/approve?allow=false&reason=QA+says+no"),
             dict(text='[[tool:calculator {"expression":"1+1"}]]'), lambda r: has(r, "QA says no")),
            ("PERM-02", spec("qa-base", permissions__defaultPolicy="deny"), dict(text='[[tool:calculator {"expression":"1+1"}]]'), lambda r: has(r, "denied by policy")),
            ("IDN-06", spec("qa-base", server__authMode="bearer"), dict(text="hi"), lambda r: r["status"] == 401),
            ("IDN-06b", spec("qa-base", server__authMode="bearer"), dict(text="hi", headers={"Authorization": "Bearer k2"}), lambda r: r["status"] == 200),
            ("MOD-18", spec("qa-base", model__model="fail-500-primary", model__retries=1,
                            model__fallbacks=[{"provider": "openai", "baseUrl": "http://127.0.0.1:18098/v1", "model": "backup-model", "apiKeyEnv": ""}]),
             dict(text="[[echo-params]]"), lambda r: '"model": "backup-model"' in r["content"]),
            ("GRD-06", spec("qa-base", guardrails__redactPII=True),
             dict(text="[[say:mail a@b.com card 4111 1111 1111 1111 ssn 123-45-6789 pi 3.14159265358979]]"),
             lambda r: r["content"] == "mail [REDACTED] card [REDACTED] ssn [REDACTED] pi 3.14159265358979"),
            ("TLP-05", spec("qa-base"), dict(text='[[tool:get_weather {"city":"Paris"}]]'), None),
        ]
        port = 8100
        for cid, s, kw, ok in cases:
            port += 1
            a = Agent(s, port)
            agents.append(a)
            try:
                if cid == "TLP-05":  # client tool passthrough needs a tools array
                    body = {"model": "qa", "messages": [{"role": "user", "content": kw["text"]}],
                            "tools": [{"type": "function", "function": {"name": "get_weather", "parameters": {"type": "object"}}}]}
                    j = json.load(urllib.request.urlopen(urllib.request.Request(
                        "http://127.0.0.1:%d/v1/chat/completions" % port, json.dumps(body).encode(), {"Content-Type": "application/json"}), timeout=30))
                    ch = j["choices"][0]
                    check(cid, ch["finish_reason"] == "tool_calls" and ch["message"]["tool_calls"][0]["function"]["name"] == "get_weather", json.dumps(ch)[:150])
                else:
                    r = a.send(kw.pop("text"), **kw)
                    check(cid, ok(r), "%s %s" % (r["status"], r["content"]))
            finally:
                a.stop()
                agents.remove(a)
    finally:
        for a in agents:
            a.stop()
        for p in fixtures:
            p.terminate()
        failed = [c for c in RESULTS if not c[1]]
        print("\n%d passed, %d failed" % (len(RESULTS) - len(failed), len(failed)))
        if failed:
            print("logs kept in", WORK)
        else:
            shutil.rmtree(WORK, ignore_errors=True)
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
