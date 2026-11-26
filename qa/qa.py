#!/usr/bin/env python3
"""QA helper for OpenHarness (Python 3 stdlib only). Run from the repo root.

  python3 qa/qa.py fixtures                      start mock LLM (:18098) + webhook server (:18099)
  python3 qa/qa.py run  SPEC [path=value ...]    build the agent binary and run it with overrides
  python3 qa/qa.py spec SPEC [path=value ...]    print the effective spec (or -o FILE)
  python3 qa/qa.py send TEXT [options]           call /v1/chat/completions and print the result
  python3 qa/qa.py inspect mock|stats|webhook|trace [-n N]
  python3 qa/qa.py reset                         clear mock + webhook history and ./qa-data

SPEC is a file path or a fixture name: qa-base, qa-kitchen-sink, qa-anthropic.
Overrides use dotted paths; values are parsed as JSON when possible:
  loop.strategy=plan_execute  loop.maxSteps=3  tools.builtins='["shell"]'  model.temperature=null

send options:
  --stream            stream and show content and agent activity separately
  --session ID        X-Session-Id header (server-side sessions)
  --user NAME         OpenAI "user" field (memory scope)
  --system TEXT       prepend a system message
  --history FILE      JSON list of prior messages to send before TEXT
  --body JSON         merge extra fields into the request body (tools, temperature, ...)
  --header 'K: V'     extra header (repeatable), e.g. --header 'Authorization: Bearer k1'
  --port N            agent port (default 8090)   --raw  print the raw response
"""
import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
FIX = os.path.join(ROOT, "qa", "fixtures")
DATA = os.path.join(ROOT, "qa-data")
MOCK, HOOKS = "http://127.0.0.1:18098", "http://127.0.0.1:18099"


def load_spec(name, overrides):
    path = name if os.path.exists(name) else os.path.join(FIX, "specs", name + (".json" if not name.endswith(".json") else ""))
    with open(path) as f:
        spec = json.load(f)
    for ov in overrides:
        key, _, raw = ov.partition("=")
        try:
            val = json.loads(raw)
        except json.JSONDecodeError:
            val = raw
        cur = spec
        parts = key.split(".")
        for p in parts[:-1]:
            cur = cur.setdefault(p, {})
        cur[parts[-1]] = val
    return spec


def http(method, url, body=None, headers=None, timeout=300):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method, headers={"Content-Type": "application/json", **(headers or {})})
    return urllib.request.urlopen(req, timeout=timeout)


def cmd_fixtures(_):
    procs = [subprocess.Popen([sys.executable, os.path.join(FIX, "mock_llm.py")]),
             subprocess.Popen([sys.executable, os.path.join(FIX, "webhook_server.py")])]
    print("fixtures running; Ctrl-C to stop")
    try:
        for p in procs:
            p.wait()
    except KeyboardInterrupt:
        for p in procs:
            p.terminate()


def cmd_spec(a):
    spec = load_spec(a.spec, a.overrides)
    out = json.dumps(spec, indent=2)
    if a.o:
        with open(a.o, "w") as f:
            f.write(out + "\n")
        print("wrote", a.o)
    else:
        print(out)


def cmd_run(a):
    spec = load_spec(a.spec, a.overrides)
    os.makedirs(DATA, exist_ok=True)
    binary = os.path.join(DATA, "bin", "harness" + (".exe" if os.name == "nt" else ""))
    os.makedirs(os.path.dirname(binary), exist_ok=True)
    print("building agent runtime …", flush=True)
    subprocess.check_call(["go", "build", "-o", binary, "./cmd/harness"], cwd=ROOT)
    fd, path = tempfile.mkstemp(prefix="qa-spec-", suffix=".json", dir=DATA)
    with os.fdopen(fd, "w") as f:
        json.dump(spec, f, indent=2)
    print("spec:", path, flush=True)
    try:
        subprocess.call([binary, "--spec", path] + (a.extra or []), cwd=ROOT)
    except KeyboardInterrupt:
        pass


def cmd_send(a):
    msgs = []
    if a.system:
        msgs.append({"role": "system", "content": a.system})
    if a.history:
        with open(a.history) as f:
            msgs.extend(json.load(f))
    msgs.append({"role": "user", "content": a.text})
    body = {"model": "qa", "messages": msgs, "stream": a.stream}
    if a.stream:
        body["stream_options"] = {"include_usage": True}
    if a.user:
        body["user"] = a.user
    if a.body:
        body.update(json.loads(a.body))
    headers = {}
    if a.session:
        headers["X-Session-Id"] = a.session
    for h in a.header or []:
        k, _, v = h.partition(":")
        headers[k.strip()] = v.strip()
    url = "http://127.0.0.1:%d/v1/chat/completions" % a.port
    try:
        r = http("POST", url, body, headers)
    except urllib.error.HTTPError as e:
        print("HTTP %d" % e.code)
        print(e.read().decode())
        sys.exit(1)
    trace = r.headers.get("X-Harness-Trace-Id")
    if not a.stream:
        raw = r.read().decode()
        if a.raw:
            print(raw)
            return
        j = json.loads(raw)
        ch = j["choices"][0]
        print(ch["message"].get("content"))
        if ch["message"].get("tool_calls"):
            print("tool_calls:", json.dumps(ch["message"]["tool_calls"]))
        print("--- finish_reason=%s usage=%s trace=%s" % (ch["finish_reason"], j.get("usage"), trace))
        return
    content, activity, finish, usage = [], [], None, None
    for line in r:
        line = line.decode().rstrip("\n")
        if a.raw and line:
            print(line)
        if not line.startswith("data: ") or line == "data: [DONE]":
            continue
        c = json.loads(line[6:])
        if "error" in c:
            print("STREAM ERROR:", c["error"])
        if c.get("usage"):
            usage = c["usage"]
        for ch in c.get("choices", []):
            d = ch.get("delta", {})
            content.append(d.get("content") or "")
            activity.append(d.get("reasoning_content") or "")
            if d.get("tool_calls"):
                print("tool_calls:", json.dumps(d["tool_calls"]))
            finish = ch.get("finish_reason") or finish
    if a.raw:
        return
    if "".join(activity).strip():
        print("=== activity\n" + "".join(activity).rstrip())
    print("=== content\n" + "".join(content))
    print("--- finish_reason=%s usage=%s trace=%s" % (finish, usage, trace))


def cmd_inspect(a):
    if a.what == "trace":
        path = os.path.join(DATA, "trace.jsonl")
        if not os.path.exists(path):
            print("no trace file at", path)
            return
        with open(path) as f:
            lines = f.readlines()[-a.n:]
        for l in lines:
            e = json.loads(l)
            print(e["type"].ljust(18), json.dumps(e["data"])[:160])
        return
    url = {"mock": MOCK + "/requests", "stats": MOCK + "/stats", "webhook": HOOKS + "/log"}[a.what]
    data = json.load(http("GET", url))
    if a.what == "stats":
        print(json.dumps(data, indent=2))
        return
    for e in data[-a.n:]:
        if a.what == "mock":
            b = e["body"]
            msgs = b.get("messages", [])
            print("%s model=%s msgs=%d tools=%d temp=%s max_tokens=%s" % (
                e["path"], e["model"], len(msgs), len(b.get("tools") or []), b.get("temperature"), b.get("max_tokens")))
        else:
            print("%s %s %s %s" % (e["method"], e["path"], json.dumps(e["query"]), json.dumps(e["json"])[:200]))


def cmd_reset(_):
    for url in (MOCK + "/reset", HOOKS + "/reset"):
        try:
            http("POST", url, {})
        except Exception as e:  # fixture not running
            print("skip", url, e)
    for sub in ("sessions", "workspace"):
        shutil.rmtree(os.path.join(DATA, sub), ignore_errors=True)
    for f in ("memory.json", "trace.jsonl"):
        try:
            os.remove(os.path.join(DATA, f))
        except FileNotFoundError:
            pass
    print("reset done")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    sub.add_parser("fixtures").set_defaults(fn=cmd_fixtures)
    p = sub.add_parser("spec"); p.add_argument("spec"); p.add_argument("overrides", nargs="*"); p.add_argument("-o"); p.set_defaults(fn=cmd_spec)
    p = sub.add_parser("run"); p.add_argument("spec"); p.add_argument("overrides", nargs="*")
    p.add_argument("--extra", nargs=argparse.REMAINDER, help="flags passed to the binary"); p.set_defaults(fn=cmd_run)
    p = sub.add_parser("send"); p.add_argument("text")
    p.add_argument("--stream", action="store_true"); p.add_argument("--session"); p.add_argument("--user"); p.add_argument("--system")
    p.add_argument("--history"); p.add_argument("--body"); p.add_argument("--header", action="append")
    p.add_argument("--port", type=int, default=8090); p.add_argument("--raw", action="store_true"); p.set_defaults(fn=cmd_send)
    p = sub.add_parser("inspect"); p.add_argument("what", choices=["mock", "stats", "webhook", "trace"])
    p.add_argument("-n", type=int, default=10); p.set_defaults(fn=cmd_inspect)
    sub.add_parser("reset").set_defaults(fn=cmd_reset)
    a = ap.parse_args()
    a.fn(a)


if __name__ == "__main__":
    main()
