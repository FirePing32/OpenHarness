# OpenHarness QA

The full test set lives in **[TEST_CASES.md](TEST_CASES.md)** (to read) and **[test-cases.csv](test-cases.csv)** (to import into a spreadsheet or test-management tool and record results). Both are generated from [`generate.py`](generate.py): edit that file, then run `python3 qa/generate.py`.

It covers every HarnessSpec parameter, the builder UI, exports for all six OS/CPU targets plus the Docker bundle, the agent binary, OpenAI API compatibility, public-mode security, the Coolify deployment, performance, and live-provider smoke tests.

## What's in this folder

| Path | Purpose |
|---|---|
| `TEST_CASES.md`, `test-cases.csv` | The test set (generated) |
| `generate.py` | Source of truth for the cases |
| `qa.py` | CLI to run agents with spec overrides, send requests and inspect what happened |
| `fixtures/mock_llm.py` | Scriptable mock LLM: OpenAI **and** Anthropic APIs, streaming, tool calls, failures |
| `fixtures/webhook_server.py` | Target for hooks, approval webhooks, HTTP tools, `http_fetch` pages and MCP over HTTP |
| `fixtures/mcp_stdio_server.py` | Minimal stdio MCP server |
| `fixtures/specs/` | `qa-base` (safe baseline), `qa-anthropic`, `qa-kitchen-sink` (every component on) |

Everything is Python 3 standard library and Go. No packages to install.

## Setup

Run everything from the repo root. You need Go 1.26+ and Python 3.9+.

```bash
python3 qa/qa.py fixtures          # terminal 1: mock LLM on :18098, webhook server on :18099
python3 qa/qa.py run qa-base       # terminal 2: agent on :8090 (builds the runtime first)
python3 qa/qa.py send "hello"      # terminal 3
```

For builder (UI) cases, also start the builder:

```bash
go run ./cmd/openharness             # local mode, http://127.0.0.1:7070
go run ./cmd/openharness --public --addr 127.0.0.1:7071   # public mode
```

## Notation used in the cases

- **`run SPEC k=v …`** means `python3 qa/qa.py run SPEC k=v …`. Overrides use dotted spec paths. Values are parsed as JSON when possible: `loop.maxSteps=3`, `model.temperature=null`, `'tools.builtins=["shell"]'`.
- **`send "TEXT" …`** means `python3 qa/qa.py send "TEXT"`. Options:
  - `--stream`: show agent activity and content separately
  - `--session ID`, `--user NAME`: session and memory scope
  - `--system TEXT`: prepend a system message
  - `--history FILE`: prior messages from a JSON file
  - `--body JSON`: extra request fields
  - `--header 'K: V'`, `--port N`
  - `--raw`: print the raw response
- **`inspect mock|stats|webhook|trace`** shows, respectively: what the model received, request counts per model, what webhooks received, and the agent's JSONL trace (`qa-data/trace.jsonl`).
- **`reset`** clears fixture history and `./qa-data` (memory, sessions, workspace, trace). Run it between cases that touch state.
- **`[[…]]` directives** script the mock LLM from the user message, so every scenario is deterministic:

| Directive | Mock behaviour |
|---|---|
| `[[tool:NAME {json}]]` | Calls a tool (repeat for parallel calls); after results: `TOOL_RESULT: …` |
| `[[loop]]` | Calls calculator forever (step / tool budgets) |
| `[[say:TEXT]]` | Replies exactly TEXT |
| `[[echo-system]]` / `[[echo-params]]` / `[[echo-messages]]` | Replies with the system prompt / sampling params, tools and headers / message roles and sizes |
| `[[slow:S]]`, `[[status:CODE]]`, `[[fail-once:CODE]]`, `[[length]]` | Latency, errors, a single failure (retries), truncation |
| `[[selfcheck:revise]]`, `[[fail-summary]]`, `[[subagent:NAME]]` | Force a self-check revision, fail the compaction summary, delegate to a sub-agent |
| model names `fail-500-*`, `fail-429-*`, `fail-401-*` | Always fail (fallback chains) |

## Environments (`Env` column)

| Code | Meaning |
|---|---|
| **L** | Local builder (`go run ./cmd/openharness`), in a browser |
| **P** | Builder in public mode (`--public`) or the deployed site |
| **B** | An agent binary: `qa.py run`, or a downloaded/exported binary |
| **D** | Docker / the Coolify deployment |

In public mode, previews **cannot** reach `127.0.0.1`. That's the SSRF guard, so the mock fixtures only work in local mode. Public-mode functional checks need a real provider key (see the LIVE section).

## Priorities and exit criteria

| Priority | Meaning |
|---|---|
| **P0** | Blocker: core flow, data safety or security. Must pass to ship. |
| **P1** | Major: an important parameter or path. A failure needs a fix or an accepted workaround. |
| **P2** | Minor: polish, edge cases, nice-to-have. |

Ship when **all P0 pass**, at least **95% of P1 pass** with the rest triaged, there are **no open security defects** (PUB-*, PERM-*, GRD-*, TLB-11/12, SES-05/10, MEM-04), and the LIVE smoke tests pass on the deployed site.

## Recording results

Import `test-cases.csv` into Google Sheets or your test tool. Fill **Status** (Pass / Fail / Blocked / N/A), **Tester**, **Date**, **Build/commit** (`git rev-parse --short HEAD` or the deployed image) and **Notes**. For failures, record the trace id (`X-Harness-Trace-Id`), the `inspect mock -n 3` output and the agent log.

## Automated coverage

`python3 qa/ci_smoke.py` runs 36 of these cases end to end: a real agent binary against the fixtures, with each result asserted. It takes about 10 s, and CI runs it on every push.

`go test ./...` already covers part of this set: the tool loop, streaming, client tools, permissions, guardrails and PII, auth, sessions (including the repeated-message regression), compaction, planning, hook injection, Anthropic conversion, the calculator, workspace escape, the public-mode sandbox, rate limiting, the SSRF guard, Dockerfile generation and the Docker bundle. The manual set focuses on UI, platform, deployment and end-to-end behaviour, and repeats the critical automated paths at the HTTP level.
