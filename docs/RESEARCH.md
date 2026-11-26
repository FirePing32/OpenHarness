# Research: what in an agent harness can be customized

> **Harness** = every piece of code, configuration and execution logic that isn't the model itself.
> (LangChain, *The Anatomy of an Agent Harness*)

This document records the research behind OpenHarness. Its main reference is **DeepSeek Harness (`dsh`)**, open-sourced under MIT on 13 Aug 2026 (`0.1.0-rc.5`), and the unofficial 131-page **DeepSeek Harness whitepaper** by FEICAI CLUB (7 parts, 38 chapters, verified against source). LangChain's anatomy article supplements it.

## 1. The core idea we borrowed: "everything is a plugin"

dsh is built on the **Cordis** kernel. Plugins contribute *services*, *typed events* and *reversible effects* to a shared context, and there is no privileged core. Even the model adapter, tool registry, session log and agent loop are plugins that can be swapped through configuration.

Each **capability seam** has three roles:

| Role | Meaning | OpenHarness equivalent |
|---|---|---|
| Service Definition | The interface (e.g. `ctx.llm`) | A section of the `HarnessSpec` |
| Service Provider | An implementation (e.g. `llm-deepseek`) | An option inside that section (e.g. `provider: openai`) |
| Consumer | Whatever uses it (often a model-facing tool) | The runtime engine |

dsh composes plugins with **profiles → bundles → `cordis.patch.yml` → `--patch` overlays**. OpenHarness collapses that stack into **one declarative JSON document (the HarnessSpec)**. The builder UI edits it, and the binary embeds it. Any binary can still be overridden at launch with `--spec file.json`, which plays the role of dsh's `--patch`.

The whitepaper counts 26 capability seams, 52 built-in tools and 44 session event types. We grouped the seams that matter for an API-served agent into the **14 components** below.

## 2. Component catalogue

| # | Component | dsh seam(s) | What is customizable in OpenHarness |
|---|---|---|---|
| 1 | **Identity & API surface** | `ctx.webServer`, profiles `web` / `headless` / `sdk` / `acp` | Name, host/port, advertised model id, bearer auth (keys via env), CORS, streaming of tool activity, request timeout |
| 2 | **Model adapter** | `ctx.llm` (`llm-deepseek`, `llm-pi-ai`), `ctx.subagentModelSelection` | Provider (OpenAI-compatible or Anthropic), base URL, model, key env var, temperature/top-p/max-tokens, reasoning effort, extra body/headers, timeout, retries, **fallback chain**, whether client sampling params are honored |
| 3 | **System prompt assembly** | `ctx.systemPrompt`, `agent-instructions` | Base prompt, ordered toggleable sections, date/time injection, tool summary, client system message policy (append / replace / ignore) |
| 4 | **Tools** | `ctx.tools` (guarded pipeline), `tool-fs`, `tool-bash`, `tool-web`, `tool-todo`, `ctx.mcpManager` | Built-ins (time, calculator, http_fetch, read/list/write file, shell, todo), **HTTP tools** (webhook-backed, JSON Schema params, `{{arg}}` URL templating, `${ENV}` headers), **MCP servers** (stdio + streamable HTTP), parallel execution and concurrency, result truncation (head / head+tail), timeouts, **client tool passthrough** (OpenAI `tools` param) |
| 5 | **Skills** | `ctx.skills`, `skill-filesystem`, `tool-skill` | Skill library (name, description, instructions); **progressive disclosure** (index in prompt + `load_skill` tool) or eager loading |
| 6 | **Memory** | `agent-instructions` (AGENTS.md), `ctx.storage` | Static notes (AGENTS.md-style), persistent store, global or per-user scope, auto-injecting N recent memories, `memory_save` / `memory_search` / `memory_delete` tools |
| 7 | **Sessions** | `ctx.sessions` (append-only `SessionEvent` log), `session-persistence-jsonl` | Stateless (OpenAI semantics) or server-side JSONL history keyed by a header or `user`; max retained messages; works with clients that send full transcripts or only the latest message |
| 8 | **Context management** | `ctx.compaction` (`compaction-basic`), `ctx.toolResultPruner`, `ctx.tokenMeter` | Context budget, trigger ratio, strategy (none / sliding window / LLM summarization), messages kept verbatim, summary prompt, stale tool-result pruning |
| 9 | **Agent loop** | `ctx.agentLoop` (`core/agent-loop`), `plan-mode`, `tool-ralph` | Strategy (ReAct / plan-then-execute / single shot), max steps, tool-call budget, wall-clock budget, behavior on tool error and on step exhaustion, planning prompt, **self-check / revision** pass |
| 10 | **Sub-agents** | `ctx.subagents` (`subagent-spawn-in-process`), `tool-subagent` | Named specialists with their own prompt, model override, tool subset and step limit, exposed as `agent_<name>` tools; isolated context; nesting depth capped at 3 |
| 11 | **Permissions & sandbox** | `ctx.approval`, `ctx.sandboxPolicy`, `ctx.permissionPresets` | Default allow/deny, per-tool and per-MCP-server policies, **webhook approval** (an API has no human to click "approve"; fails closed), workspace root jail, read-only files, shell command allowlist, fetch domain allowlist |
| 12 | **Guardrails** | `agent/pre-step` and `tools/pre-execute` waterfalls | Input and output rules (regex or keyword; block / redact / warn), PII redaction, max input size, blocked message |
| 13 | **Hooks** | `agent/pre-step`, `tools/pre-execute`, `tools/post-execute`, `agent/turn-stopping` | Lifecycle events `turn_start`, `pre_step`, `pre_tool`, `post_tool`, `turn_end`, `on_error`; hook types webhook (can block or inject context), log, or static context injection; blocking or fire-and-forget |
| 14 | **Observability** | `ctx.sessionTelemetry` (`session-telemetry-otel`), `telemetry/*` events | Log level and format, JSONL event trace (replayable), trace-id header, usage reporting (summed across sub-agents, compaction and self-check calls) |

## 3. The turn lifecycle (from dsh's `docs/architecture.md`, as implemented)

dsh: a **step** is one model request plus the tools it calls; a **turn** is zero or more steps.

```
turn_start hook ─▶ input guardrails ─▶ load session ─▶ assemble system prompt
      │
      ▼
 [plan_execute only] planning call (no tools) ─▶ plan inserted before the user turn
      │
      ▼
┌──────────── step loop (≤ maxSteps, ≤ maxToolCalls, ≤ timeBudget) ────────────┐
│ prune stale tool results ─▶ compact if over budget ─▶ pre_step hook          │
│ ─▶ model call (streamed) ─▶ no tool calls? ──────────────────────▶ exit      │
│ ─▶ client tool calls? return them to the caller (finish_reason=tool_calls)  │
│ ─▶ for each tool: permission ─▶ pre_tool hook ─▶ run ─▶ truncate ─▶ post_tool│
└──────────────────────────────────────────────────────────────────────────────┘
      │ (exhausted → "summarize" final call or error)
      ▼
 self-check / revise ─▶ output guardrails ─▶ append to session log ─▶ turn_end hook
```

Following the dsh principle "model-visible means logged", every model call, tool result, compaction and guardrail decision is written as an event to the trace file when one is configured.

## 4. Where we deliberately differ from dsh

| dsh | OpenHarness | Why |
|---|---|---|
| Plugins run in the same Node process, configured by patch files | One Go binary per agent, configured by an embedded spec | You asked for a downloadable binary. A static Go binary has no runtime dependencies and cross-compiles to 6 OS/arch targets. |
| Interfaces: Web UI, headless, JSON-RPC SDK, ACP | **OpenAI-compatible** `/v1/chat/completions` (+ streaming, tools) and `/v1/models` | None of the sources describe an OpenAI-compatible server in dsh. That is the integration surface you asked for. |
| Human approval prompts | Allow/deny rules + approval webhook | An HTTP API has no interactive user. |
| Web server has no auth (the whitepaper's criticism of rc.5) | Optional bearer auth, keys read from env, constant-time compare | We took the whitepaper's criticism on board. |
| Telemetry endpoint hard-coded (the whitepaper's criticism) | Telemetry is local-only (stderr + JSONL file) | Nothing leaves the machine unless you add a webhook hook. |

## 5. Ideas not yet implemented (candidates for later)

- Agent teams / shared task boards (`ctx.agentTeams`), scheduling & triggers (`ctx.schedule`, `webhook-github`)
- Code-mode execution (`ctx.ptcRuntime`: the model writes code that calls tools)
- Remote sandboxes (`sandbox-ssh`, `fs-ssh`): today the sandbox is a workspace-root jail plus allowlists
- OpenTelemetry export, LSP, browser/computer use, `/v1/responses` API
- A plugin ABI for custom Go tools compiled into the binary

## Sources

- DeepSeek Harness repository: https://github.com/deepseek-ai/deepseek-harness (README, `docs/architecture.md`, `docs/capability-seams.md`, `SAFETY.md`)
- DeepSeek Harness whitepaper listing: https://agentsmd.io/deepseek-harness/plugins/deepseek-harness-whitepaper
- DeepSeek product page: https://deepseek.com/en/harness/
- Eigent: https://www.eigent.ai/blog/deepseek-harness-agent-runtime
- Digital Applied: https://www.digitalapplied.com/blog/deepseek-harness-open-source-agent-framework-2026
- LangChain, The Anatomy of an Agent Harness: https://www.langchain.com/blog/the-anatomy-of-an-agent-harness

*Caveat: the whitepaper's own chapters (PDF / web edition) could not be fetched. What we know of it comes from its listing page, and the seam details come from the dsh repository docs it is based on.*
