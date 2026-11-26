#!/usr/bin/env python3
"""Scriptable mock LLM for OpenHarness QA (Python 3 stdlib only).

Serves OpenAI-compatible  POST /v1/chat/completions  and
Anthropic-compatible       POST /v1/messages        (both streaming and not).

Behaviour is driven by directives in the LAST USER message, so a tester can
reproduce any scenario from the playground or curl:

  [[tool:NAME {"json":"args"}]]   call tool NAME (repeat the directive for parallel calls);
                                  after the tool results arrive, reply "TOOL_RESULT: <content>"
  [[loop]]                        call calculator forever (step / tool-call budget tests)
  [[say:TEXT]]                    reply exactly TEXT (guardrail / PII tests)
  [[echo-system]]                 reply with the system prompt the harness assembled
  [[echo-params]]                 reply with the JSON of sampling params, tools and headers received
  [[echo-messages]]               reply with the roles/lengths of every message received
  [[slow:SECONDS]]                sleep before answering (timeouts, keep-alive, time budget)
  [[status:CODE]]                 fail every request for this message with HTTP CODE
  [[fail-once:CODE]]              fail the first request for this message, then answer (retries)
  [[length]]                      finish with finish_reason=length
  [[selfcheck:revise]]            when the harness runs its self-check, reply "REVISED ANSWER"
  [[fail-summary]]                put in an EARLY message: the compaction summary call gets HTTP 500
  [[subagent:NAME]]               call tool agent_NAME with a task that makes the sub-agent
                                  run calculator(2^8); "[[subagent:NAME TOOL]]" asks for TOOL instead
                                  (use a tool outside the sub-agent's allow-list to test scoping)

Harness-internal calls are recognised automatically:
  planning prompt      -> "1. Step one\\n2. Step two"
  self-check prompt    -> "APPROVED" (or "REVISED ANSWER", see above)
  compaction summary   -> "SUMMARY OF <n> CHARS"
  step-limit final     -> "FINAL AFTER STEP LIMIT"

Model names select failure modes for fallback/retry tests:
  fail-500-*  fail-429-*  fail-401-*   always fail with that status
  anything else                        normal behaviour

Inspection endpoints:
  GET  /requests   last 200 requests (path, model, headers subset, body)
  GET  /stats      request counts per model
  POST /reset      clear history, counters and fail-once state

Usage:  python3 mock_llm.py [--port 18098]
"""
import argparse
import json
import re
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

LOCK = threading.Lock()
REQUESTS = []
STATS = {}
FAILED_ONCE = set()

DIRECTIVE = re.compile(r"\[\[([a-z\-]+)(?::(.*?))?\]\]", re.S)


def directives(text):
    out = []
    for m in DIRECTIVE.finditer(text or ""):
        out.append((m.group(1), (m.group(2) or "").strip()))
    return out


def text_of(content):
    if content is None:
        return ""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts = []
        for p in content:
            if isinstance(p, dict):
                if p.get("type") == "text":
                    parts.append(p.get("text", ""))
                elif p.get("type") == "tool_result":
                    parts.append(text_of(p.get("content")))
        return "\n".join(parts)
    return str(content)


def normalize(path, body):
    """Return (system, messages[{role,text,tool_calls}], model) for either API."""
    model = body.get("model", "")
    msgs = []
    if path.endswith("/messages"):  # Anthropic
        system = text_of(body.get("system"))
        for m in body.get("messages", []):
            content = m.get("content")
            if isinstance(content, list) and any(isinstance(p, dict) and p.get("type") == "tool_result" for p in content):
                for p in content:
                    if p.get("type") == "tool_result":
                        msgs.append({"role": "tool", "text": text_of(p.get("content"))})
                continue
            calls = [p for p in content if isinstance(p, dict) and p.get("type") == "tool_use"] if isinstance(content, list) else []
            msgs.append({"role": m.get("role"), "text": text_of(content), "tool_calls": calls})
    else:  # OpenAI
        system_parts = []
        for m in body.get("messages", []):
            if m.get("role") in ("system", "developer"):
                system_parts.append(text_of(m.get("content")))
                msgs.append({"role": "system", "text": text_of(m.get("content"))})
            else:
                msgs.append({"role": m.get("role"), "text": text_of(m.get("content")), "tool_calls": m.get("tool_calls") or []})
        system = system_parts[0] if system_parts else ""
    return system, msgs, model


def decide(path, body, headers):
    """Return a dict: {status} | {text, finish} | {calls:[(name,args)], finish}."""
    system, msgs, model = normalize(path, body)
    for code in ("500", "429", "401"):
        if model.startswith("fail-" + code):
            return {"status": int(code)}

    convo = [m for m in msgs if m["role"] != "system"]
    last = convo[-1] if convo else {"role": "user", "text": ""}
    users = [m for m in convo if m["role"] == "user"]
    last_user = users[-1]["text"] if users else ""
    d = directives(last_user)
    names = [k for k, _ in d]
    arg = {k: v for k, v in d}

    has_tools = bool(body.get("tools"))
    lt = last["text"] if last["role"] == "user" else ""

    # Harness-internal prompts.
    if "Summarize the conversation so far" in system or "Summarize the conversation" in (msgs[0]["text"] if msgs else ""):
        if "[[fail-summary]]" in lt:
            return {"status": 500}
        return {"text": "SUMMARY OF %d CHARS" % len(lt), "finish": "stop"}
    if "reached the step limit" in lt:
        return {"text": "FINAL AFTER STEP LIMIT", "finish": "stop"}
    if "write a short numbered plan" in lt.lower():
        return {"text": "1. Step one\n2. Step two", "finish": "stop"}
    if "reply with exactly APPROVED" in lt or "APPROVED" in lt and "Review your draft" in lt:
        revise = any("selfcheck" in k and v == "revise" for u in users for k, v in directives(u["text"]))
        return {"text": "REVISED ANSWER" if revise else "APPROVED", "finish": "stop"}

    # Failure injection (keyed by the user text so retries hit the same key).
    if "status" in arg:
        return {"status": int(arg["status"] or 500)}
    if "fail-once" in arg:
        key = (model, last_user)
        with LOCK:
            first = key not in FAILED_ONCE
            FAILED_ONCE.add(key)
        if first:
            return {"status": int(arg["fail-once"] or 500)}
    if "slow" in arg:
        try:
            time.sleep(float(arg["slow"]))
        except ValueError:
            pass

    if "echo-system" in names:
        return {"text": system, "finish": "stop"}
    if "echo-params" in names:
        info = {
            "model": model,
            "temperature": body.get("temperature"),
            "top_p": body.get("top_p"),
            "max_tokens": body.get("max_tokens"),
            "reasoning_effort": body.get("reasoning_effort"),
            "tool_choice": body.get("tool_choice"),
            "tools": [t.get("function", t).get("name") for t in body.get("tools") or []],
            "extra_keys": sorted(k for k in body if k not in (
                "model", "messages", "stream", "stream_options", "temperature", "top_p", "max_tokens",
                "reasoning_effort", "tools", "tool_choice", "system")),
            "headers": {k: v for k, v in headers.items() if k.lower().startswith("x-") or k.lower() in ("authorization", "x-api-key", "anthropic-version")},
        }
        if info["headers"].get("Authorization"):
            info["headers"]["Authorization"] = info["headers"]["Authorization"][:12] + "…"
        if info["headers"].get("X-Api-Key"):
            info["headers"]["X-Api-Key"] = info["headers"]["X-Api-Key"][:6] + "…"
        return {"text": json.dumps(info, sort_keys=True), "finish": "stop"}
    if "echo-messages" in names:
        rows = ["%s:%d" % (m["role"], len(m["text"])) for m in msgs]
        return {"text": "MESSAGES %d | %s" % (len(msgs), " ".join(rows)), "finish": "stop"}
    if "length" in names:
        return {"text": "This answer was cut off", "finish": "length"}

    tool_dirs = [v for k, v in d if k == "tool"]
    for v in [v for k, v in d if k == "subagent"]:
        name, _, tool = v.partition(" ")
        if tool.strip() and tool.strip() != "calculator":
            task = "SUBTASK [[tool:%s {}]]" % tool.strip()
        else:
            task = 'SUBTASK [[tool:calculator {"expression":"2^8"}]]'
        tool_dirs.append("agent_%s %s" % (name, json.dumps({"task": task})))
    if "loop" in names and has_tools:
        return {"calls": [("calculator", {"expression": "1+1"})], "finish": "tool_calls"}
    if tool_dirs and has_tools:
        if last["role"] == "tool":
            results = []
            for m in reversed(convo):
                if m["role"] != "tool":
                    break
                results.append(m["text"])
            return {"text": "TOOL_RESULT: " + " || ".join(reversed(results)), "finish": "stop"}
        calls = []
        for v in tool_dirs:
            name, _, raw = v.partition(" ")
            try:
                args = json.loads(raw) if raw.strip() else {}
            except json.JSONDecodeError:
                args = {"_raw": raw}
            calls.append((name, args))
        return {"calls": calls, "finish": "tool_calls"}
    if "say" in arg:
        return {"text": arg["say"], "finish": "stop"}
    return {"text": "Hello from mock (model=%s)" % model, "finish": "stop"}


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def _json(self, code, obj):
        b = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def do_GET(self):
        if self.path.startswith("/requests"):
            with LOCK:
                return self._json(200, REQUESTS[-200:])
        if self.path.startswith("/stats"):
            with LOCK:
                return self._json(200, STATS)
        self._json(404, {"error": "not found"})

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(n) if n else b"{}"
        if self.path.startswith("/reset"):
            with LOCK:
                REQUESTS.clear(); STATS.clear(); FAILED_ONCE.clear()
            return self._json(200, {"ok": True})
        try:
            body = json.loads(raw or b"{}")
        except json.JSONDecodeError:
            return self._json(400, {"error": {"message": "bad json"}})
        hdrs = {k: v for k, v in self.headers.items()}
        model = body.get("model", "")
        with LOCK:
            REQUESTS.append({"path": self.path, "model": model, "headers": hdrs, "body": body, "ts": time.time()})
            STATS[model] = STATS.get(model, 0) + 1
        anthropic = self.path.rstrip("/").endswith("/messages")
        if not (anthropic or self.path.rstrip("/").endswith("/chat/completions")):
            return self._json(404, {"error": {"message": "unknown path " + self.path}})
        r = decide(self.path, body, hdrs)
        if "status" in r:
            return self._json(r["status"], {"error": {"message": "mock injected HTTP %d" % r["status"], "type": "mock_error"}})
        if anthropic:
            return self._anthropic(body, r)
        return self._openai(body, r)

    # -------------------------------------------------------------- OpenAI
    def _openai(self, body, r):
        model = body.get("model", "")
        usage = {"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}
        if not body.get("stream"):
            msg = {"role": "assistant", "content": r.get("text")}
            if "calls" in r:
                msg["content"] = None
                msg["tool_calls"] = [{"id": "call_%d" % i, "type": "function", "function": {"name": n, "arguments": json.dumps(a)}} for i, (n, a) in enumerate(r["calls"])]
            return self._json(200, {"id": "mock", "object": "chat.completion", "model": model,
                                    "choices": [{"index": 0, "message": msg, "finish_reason": r["finish"]}], "usage": usage})
        self._sse_start()
        if "calls" in r:
            for i, (n, a) in enumerate(r["calls"]):
                self._sse({"choices": [{"index": 0, "delta": {"tool_calls": [{"index": i, "id": "call_%d_%d" % (int(time.time() * 1000) % 100000, i), "type": "function", "function": {"name": n, "arguments": ""}}]}}]})
                args = json.dumps(a)
                half = len(args) // 2
                for chunk in (args[:half], args[half:]):  # split arguments to exercise reassembly
                    self._sse({"choices": [{"index": 0, "delta": {"tool_calls": [{"index": i, "function": {"arguments": chunk}}]}}]})
        else:
            text = r["text"]
            for i in range(0, len(text), 12):
                self._sse({"choices": [{"index": 0, "delta": {"content": text[i:i + 12]}}]})
        self._sse({"choices": [{"index": 0, "delta": {}, "finish_reason": r["finish"]}]})
        self._sse({"choices": [], "usage": usage})
        self._raw("data: [DONE]\n\n")
        self._end()

    # -------------------------------------------------------------- Anthropic
    def _anthropic(self, body, r):
        model = body.get("model", "")
        stop = {"stop": "end_turn", "length": "max_tokens", "tool_calls": "tool_use"}[r["finish"]]
        if not body.get("stream"):
            content = [{"type": "text", "text": r.get("text", "")}] if "calls" not in r else [
                {"type": "tool_use", "id": "toolu_%d" % i, "name": n, "input": a} for i, (n, a) in enumerate(r["calls"])]
            return self._json(200, {"id": "msg_mock", "type": "message", "role": "assistant", "model": model, "content": content,
                                    "stop_reason": stop, "usage": {"input_tokens": 11, "output_tokens": 7}})
        self._sse_start()
        self._sse({"type": "message_start", "message": {"id": "msg_mock", "model": model, "usage": {"input_tokens": 11, "output_tokens": 0}}}, "message_start")
        if "calls" in r:
            for i, (n, a) in enumerate(r["calls"]):
                self._sse({"type": "content_block_start", "index": i, "content_block": {"type": "tool_use", "id": "toolu_%d" % i, "name": n, "input": {}}}, "content_block_start")
                args = json.dumps(a)
                half = len(args) // 2
                for chunk in (args[:half], args[half:]):
                    self._sse({"type": "content_block_delta", "index": i, "delta": {"type": "input_json_delta", "partial_json": chunk}}, "content_block_delta")
                self._sse({"type": "content_block_stop", "index": i}, "content_block_stop")
        else:
            self._sse({"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}, "content_block_start")
            text = r["text"]
            for i in range(0, len(text), 12):
                self._sse({"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": text[i:i + 12]}}, "content_block_delta")
            self._sse({"type": "content_block_stop", "index": 0}, "content_block_stop")
        self._sse({"type": "message_delta", "delta": {"stop_reason": stop}, "usage": {"output_tokens": 7}}, "message_delta")
        self._sse({"type": "message_stop"}, "message_stop")
        self._end()

    # -------------------------------------------------------------- SSE helpers
    def _sse_start(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.end_headers()

    def _raw(self, s):
        self.wfile.write(s.encode())
        self.wfile.flush()

    def _sse(self, obj, event=None):
        if event:
            self._raw("event: %s\n" % event)
        self._raw("data: %s\n\n" % json.dumps(obj))

    def _end(self):
        self.close_connection = True


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--port", type=int, default=18098)
    ap.add_argument("--host", default="127.0.0.1")
    a = ap.parse_args()
    print("mock LLM on http://%s:%d  (OpenAI: /v1/chat/completions, Anthropic: /v1/messages)" % (a.host, a.port), flush=True)
    ThreadingHTTPServer((a.host, a.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
