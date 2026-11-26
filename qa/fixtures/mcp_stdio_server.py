#!/usr/bin/env python3
"""Minimal stdio MCP server for OpenHarness QA (stdlib only).

Tools:
  echo {text}     -> "echo: <text>"
  add {a, b}      -> a + b
  env {name}      -> value of an environment variable (checks the spec's env injection)
  fail {}         -> an isError result
  slow {seconds}  -> sleeps, then "done" (tool timeout tests)

Writes one line to stderr at start-up (visible in the harness log at debug level).
Spec entry:  {"name": "qa", "transport": "stdio", "command": "python3",
              "args": ["qa/fixtures/mcp_stdio_server.py"], "env": {"MCP_QA_VAR": "hello"}}
"""
import json
import os
import sys
import time

TOOLS = [
    {"name": "echo", "description": "Echo the given text.",
     "inputSchema": {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}},
    {"name": "add", "description": "Add two numbers.",
     "inputSchema": {"type": "object", "properties": {"a": {"type": "number"}, "b": {"type": "number"}}, "required": ["a", "b"]}},
    {"name": "env", "description": "Read an environment variable.",
     "inputSchema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}},
    {"name": "fail", "description": "Always fails.", "inputSchema": {"type": "object", "properties": {}}},
    {"name": "slow", "description": "Sleep for N seconds.",
     "inputSchema": {"type": "object", "properties": {"seconds": {"type": "number"}}}},
]


def call(name, args):
    if name == "echo":
        return {"content": [{"type": "text", "text": "echo: " + str(args.get("text", ""))}]}
    if name == "add":
        return {"content": [{"type": "text", "text": str(args.get("a", 0) + args.get("b", 0))}]}
    if name == "env":
        return {"content": [{"type": "text", "text": os.environ.get(args.get("name", ""), "<unset>")}]}
    if name == "fail":
        return {"content": [{"type": "text", "text": "deliberate MCP failure"}], "isError": True}
    if name == "slow":
        time.sleep(float(args.get("seconds", 5)))
        return {"content": [{"type": "text", "text": "done"}]}
    raise KeyError(name)


def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()


def main():
    sys.stderr.write("qa stdio MCP server started\n")
    sys.stderr.flush()
    for line in sys.stdin:
        try:
            msg = json.loads(line)
        except json.JSONDecodeError:
            continue
        if "id" not in msg:
            continue  # notifications
        method, params = msg.get("method"), msg.get("params") or {}
        try:
            if method == "initialize":
                result = {"protocolVersion": params.get("protocolVersion", "2025-06-18"),
                          "capabilities": {"tools": {}}, "serverInfo": {"name": "qa-stdio-mcp", "version": "1.0"}}
            elif method == "tools/list":
                result = {"tools": TOOLS}
            elif method == "tools/call":
                result = call(params.get("name"), params.get("arguments") or {})
            else:
                raise KeyError(method)
            send({"jsonrpc": "2.0", "id": msg["id"], "result": result})
        except KeyError as e:
            send({"jsonrpc": "2.0", "id": msg["id"], "error": {"code": -32601, "message": "unknown: %s" % e}})


if __name__ == "__main__":
    main()
