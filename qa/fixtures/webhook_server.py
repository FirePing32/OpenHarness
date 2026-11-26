#!/usr/bin/env python3
"""Webhook / HTTP-tool / MCP-over-HTTP fixture for OpenHarness QA (stdlib only).

Endpoints (behaviour is chosen with query parameters, so a spec can point
different hooks at different behaviours):

  POST /hook      lifecycle hook target. Query: action=block, message=TEXT,
                  inject=TEXT, delay=MS, status=CODE. Records every payload.
  POST /approve   permission webhook. Query: allow=true|false, reason=TEXT,
                  delay=MS, status=CODE, invalid=1 (returns non-JSON).
  ANY  /tool      HTTP-tool target. Echoes method, path, query, JSON body and
                  headers. Query: status=CODE, big=N (pad the reply to N chars).
  GET  /page      small HTML page (title, script, style) for http_fetch tests.
  GET  /big       ~500 KB of text for maxBytes / truncation tests.
  POST /mcp       minimal MCP server over streamable HTTP (tools: echo, add,
                  fail). Query: sse=1 answers as text/event-stream.

Inspection:
  GET  /log       everything received, newest last
  POST /reset     clear the log

Usage:  python3 webhook_server.py [--port 18099]
"""
import argparse
import json
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

LOCK = threading.Lock()
LOG = []

PAGE = """<!doctype html><html><head><title>QA Page</title>
<style>body{color:red}</style><script>var secret = "should-not-appear";</script></head>
<body><h1>OpenHarness QA</h1><p>The magic number is <b>4242</b>.</p>
<ul><li>alpha</li><li>beta</li></ul></body></html>"""

MCP_TOOLS = [
    {"name": "echo", "description": "Echo the given text back.",
     "inputSchema": {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}},
    {"name": "add", "description": "Add two numbers.",
     "inputSchema": {"type": "object", "properties": {"a": {"type": "number"}, "b": {"type": "number"}}, "required": ["a", "b"]}},
    {"name": "fail", "description": "Always returns an error result.",
     "inputSchema": {"type": "object", "properties": {}}},
]


def mcp_result(method, params):
    if method == "initialize":
        return {"protocolVersion": params.get("protocolVersion", "2025-06-18"),
                "capabilities": {"tools": {}}, "serverInfo": {"name": "qa-http-mcp", "version": "1.0"}}
    if method == "tools/list":
        return {"tools": MCP_TOOLS}
    if method == "tools/call":
        name, args = params.get("name"), params.get("arguments") or {}
        if name == "echo":
            return {"content": [{"type": "text", "text": "echo: " + str(args.get("text", ""))}]}
        if name == "add":
            return {"content": [{"type": "text", "text": str(args.get("a", 0) + args.get("b", 0))}]}
        if name == "fail":
            return {"content": [{"type": "text", "text": "deliberate MCP failure"}], "isError": True}
        raise KeyError(name)
    raise KeyError(method)


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def _send(self, code, body, ctype="application/json", extra=None):
        b = body.encode() if isinstance(body, str) else body
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(b)))
        for k, v in (extra or {}).items():
            self.send_header(k, v)
        self.end_headers()
        self.wfile.write(b)

    def _json(self, code, obj, extra=None):
        self._send(code, json.dumps(obj), "application/json", extra)

    def _read(self):
        n = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(n) if n else b""
        try:
            return json.loads(raw) if raw else None, raw
        except json.JSONDecodeError:
            return None, raw

    def _record(self, body, raw):
        u = urlparse(self.path)
        with LOCK:
            LOG.append({"ts": time.time(), "method": self.command, "path": u.path, "query": parse_qs(u.query),
                        "headers": {k: v for k, v in self.headers.items()}, "json": body,
                        "raw": raw.decode("utf-8", "replace")[:2000] if body is None else None})

    def do_GET(self):
        self._dispatch()

    def do_DELETE(self):
        self._dispatch()

    def do_PUT(self):
        self._dispatch()

    def do_POST(self):
        self._dispatch()

    def _dispatch(self):
        u = urlparse(self.path)
        q = {k: v[0] for k, v in parse_qs(u.query).items()}
        body, raw = self._read() if self.command in ("POST", "PUT", "DELETE") else (None, b"")
        if u.path == "/log":
            with LOCK:
                return self._json(200, LOG)
        if u.path == "/reset":
            with LOCK:
                LOG.clear()
            return self._json(200, {"ok": True})
        self._record(body, raw)
        if "delay" in q:
            time.sleep(int(q["delay"]) / 1000)
        if "status" in q and u.path != "/mcp":
            return self._json(int(q["status"]), {"error": "injected HTTP " + q["status"]})

        if u.path == "/hook":
            out = {}
            if q.get("action") == "block":
                out = {"action": "block", "message": q.get("message", "Blocked by QA hook")}
            if "inject" in q:
                out["inject"] = q["inject"]
            return self._json(200, out)
        if u.path == "/approve":
            if q.get("invalid") == "1":
                return self._send(200, "not json", "text/plain")
            return self._json(200, {"allow": q.get("allow", "true") == "true", "reason": q.get("reason", "")})
        if u.path == "/tool":
            reply = {"method": self.command, "query": {k: v for k, v in q.items()}, "json": body,
                     "auth": self.headers.get("Authorization"), "x_qa": self.headers.get("X-QA")}
            text = json.dumps(reply)
            if "big" in q:
                text = text + " " + ("x" * max(0, int(q["big"]) - len(text)))
                return self._send(200, text, "text/plain")
            return self._json(200, reply)
        if u.path == "/page":
            return self._send(200, PAGE, "text/html; charset=utf-8")
        if u.path == "/big":
            return self._send(200, ("0123456789" * 50000), "text/plain")
        if u.path == "/mcp":
            return self._mcp(body, q)
        return self._json(404, {"error": "unknown path " + u.path})

    def _mcp(self, msg, q):
        if not isinstance(msg, dict):
            return self._json(400, {"error": "expected JSON-RPC"})
        sid = self.headers.get("Mcp-Session-Id") or str(uuid.uuid4())
        if "id" not in msg:  # notification
            return self._send(202, b"", "application/json", {"Mcp-Session-Id": sid})
        try:
            resp = {"jsonrpc": "2.0", "id": msg["id"], "result": mcp_result(msg.get("method"), msg.get("params") or {})}
        except KeyError as e:
            resp = {"jsonrpc": "2.0", "id": msg["id"], "error": {"code": -32601, "message": "unknown: %s" % e}}
        if q.get("sse") == "1":
            body = "event: message\ndata: %s\n\n" % json.dumps(resp)
            return self._send(200, body, "text/event-stream", {"Mcp-Session-Id": sid})
        return self._json(200, resp, {"Mcp-Session-Id": sid})


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--port", type=int, default=18099)
    ap.add_argument("--host", default="127.0.0.1")
    a = ap.parse_args()
    print("QA webhook server on http://%s:%d" % (a.host, a.port), flush=True)
    ThreadingHTTPServer((a.host, a.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
