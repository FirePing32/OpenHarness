#!/usr/bin/env python3
"""Source of truth for the OpenHarness QA test set.

Edit the CASES below, then run:  python3 qa/generate.py
It writes qa/TEST_CASES.md (to read) and qa/test-cases.csv (to import into a
spreadsheet or test-management tool and record results).

Notation used in steps (see qa/README.md):
  run SPEC k=v ...   python3 qa/qa.py run SPEC k=v ...   (agent on :8090)
  send "TEXT" ...    python3 qa/qa.py send "TEXT" ...
  [[...]]            mock-LLM directives (qa/fixtures/mock_llm.py)
  Env L / P / B / D  local builder, public builder, built binary, deployed (Coolify)
"""
import csv
import os
from collections import OrderedDict

SECTIONS = OrderedDict()
CASES = []


def section(code, title, intro=""):
    SECTIONS[code] = (title, intro)


def tc(id, title, pri, typ, env, setup, steps, expected):
    CASES.append(dict(id=id, section=id.split("-")[0], title=title, priority=pri, type=typ, env=env,
                      setup=setup, steps=steps, expected=expected))


# =====================================================================
section("UI", "Builder shell, navigation and persistence",
        "The builder web app around the component steps. Env L unless stated.")
tc("UI-01", "Builder loads with no console errors", "P0", "Smoke", "L",
   "Builder running: `go run ./cmd/openharness`.",
   ["Open http://127.0.0.1:7070.", "Open the browser console."],
   "Start screen renders (hero, 14-component grid, preset cards). Top bar shows \"builder connected\". No console errors.")
tc("UI-02", "Backend offline state", "P1", "Negative", "L",
   "Builder open in the browser.",
   ["Stop the builder process.", "Reload the page.", "Restart the builder and reload."],
   "While stopped: offline status, presets unavailable message, build/test disabled with a 'backend is not reachable' callout. After restart everything works again.")
tc("UI-03", "Sidebar navigation to every step", "P0", "Functional", "L",
   "Any preset selected.",
   ["Click each of the 16 sidebar entries (14 components, Test, Build & download)."],
   "Each step renders its title, summary, 'Equivalent in DeepSeek Harness' chip and 'Why it matters' note (component steps). No stray text such as 'false', 'undefined' or '[object Object]'.")
tc("UI-04", "Back / Next buttons", "P1", "Functional", "L", "Any preset selected.",
   ["On step 1 press Next repeatedly until Build & download.", "Press Back to step 1."],
   "Next/Back move one step at a time; the first step has no Back, the last has no Next. Visited steps get a check mark in the sidebar.")
tc("UI-05", "Draft autosave and resume", "P0", "Functional", "L", "Any preset selected.",
   ["Change the agent name to `resume-test` on Identity.", "Go to step 5.", "Reload the page."],
   "The draft resumes on the same step with `resume-test` and the same visited marks.")
tc("UI-06", "Brand logo returns to start with Resume", "P2", "Functional", "L", "A draft in progress.",
   ["Click the OpenHarness logo.", "Click Resume."],
   "Start screen shows 'Continue your draft <name>'. Resume returns to the draft unchanged.")
tc("UI-07", "Reset discards the draft", "P1", "Functional", "L", "A draft in progress.",
   ["Click Reset, cancel the confirm.", "Click Reset, accept."],
   "Cancel: nothing changes. Accept: confirm text 'Discard this agent and start over?…', then the start screen with no Resume card.")
tc("UI-08", "Spec JSON live panel", "P1", "Functional", "L", "Desktop width ≥1200px.",
   ["Click Spec JSON.", "Change model.temperature to 0.9 on the Model step.", "Close the panel (button or Escape on narrow screens)."],
   "Panel shows pretty-printed spec and updates immediately to `\"temperature\": 0.9`. Toggle state is reflected on the button.")
tc("UI-09", "Theme toggle follows system then remembers choice", "P1", "Functional", "L",
   "Clear site data. OS in dark mode.",
   ["Load the app.", "Click the theme button.", "Reload.", "Switch the OS to light mode."],
   "Loads dark (follows OS). Click switches to light, icon becomes a moon, label 'Switch to dark theme'. After reload it stays light with no flash of dark. OS change no longer affects it.")
tc("UI-10", "Theme visual check (both themes)", "P2", "UI", "L", "",
   ["Visit start screen, a component step, Test and Build in light and dark."],
   "Monochrome palette, square corners, bold corner brackets on cards, readable contrast, grid background, wordmark fades. Only errors use red.")
tc("UI-11", "Phone layout (375px)", "P1", "UI", "L", "Browser devtools at 375×812.",
   ["Visit start screen and several steps.", "Use the step dropdown that replaces the sidebar."],
   "No horizontal scroll; top bar on one row (Spec JSON hidden); forms single-column; step dropdown navigates.")
tc("UI-12", "Tablet layout (768px)", "P2", "UI", "L", "Viewport 768×1024.",
   ["Visit start screen, Tools, Test, Build."], "Layouts adapt without overlap or clipped controls.")
tc("UI-13", "Keyboard-only navigation", "P1", "Accessibility", "L", "",
   ["Tab through the top bar, sidebar and a form step.", "Toggle a switch with Space, pick a radio card with arrows.", "Use the skip link."],
   "Every control is reachable with a visible focus ring; switches and radio cards operate from the keyboard; skip link jumps to content.")
tc("UI-14", "Screen-reader labels", "P2", "Accessibility", "L", "VoiceOver/NVDA.",
   ["Navigate the Model step and the theme button."],
   "Inputs announce their labels and help text; theme button announces 'Switch to light/dark theme'.")
tc("UI-15", "Field validation errors surface in sidebar", "P0", "Functional", "L", "",
   ["Set agent name to `bad name!`.", "Wait for validation."],
   "Identity shows a problem callout listing `name: use 1-64 letters, digits, '-' or '_'`; sidebar shows an error badge on Identity; 'Spec is valid.' disappears.")
tc("UI-16", "Error links jump to the right step", "P1", "Functional", "L", "Spec with errors in model and loop (e.g. model.model empty, loop.maxSteps 0).",
   ["Open Build & download.", "Click each listed problem."], "Each error navigates to the component that owns it (model.* → Model, loop.* → Agent loop).")
tc("UI-17", "Rapid typing keeps focus", "P1", "UI", "L", "",
   ["Type quickly into the system prompt and a number field."], "Focus and caret position are kept across live re-renders; no characters lost.")
tc("UI-18", "Storage blocked / private window", "P2", "Negative", "L", "Private window or site data blocked.",
   ["Use the app end to end."], "App works; drafts and theme just aren't remembered. No errors.")

# =====================================================================
section("PRE", "Start screen, presets, import and export")
tc("PRE-01", "Preset cards show correct metadata", "P1", "Functional", "L", "",
   ["Read each preset card's chips."],
   "Minimal: single shot · 0 tools · stateless. Research: plan execute · 4 tools · stateless · 1 sub-agent. Coding: react · 5 tools · server sessions. Support: react · 2 tools · server sessions · 1 skill.")
tc("PRE-02", "Each preset loads a valid spec", "P0", "Functional", "L", "",
   ["For each of the 4 presets: select it, open Build & download."], "Pre-flight check: 'All checks passed. Ready to build.' for every preset.")
tc("PRE-03", "Start from defaults", "P1", "Functional", "L", "", ["Click 'Start from defaults'."],
   "Spec equals server defaults: DeepSeek model, ReAct, current_time + calculator, stateless sessions.")
tc("PRE-04", "Start building CTA scrolls to presets", "P2", "UI", "L", "", ["Click START BUILDING ↗."], "Page scrolls smoothly to 'Start from a preset'.")
tc("PRE-05", "Export spec JSON", "P0", "Functional", "L", "Any draft named `exp-test`.",
   ["Click Export JSON (top bar) and 'Export spec JSON' on Build."], "Downloads `exp-test.harness.json`, pretty-printed, identical to the Spec JSON panel.")
tc("PRE-06", "Import round-trip", "P0", "Functional", "L", "File from PRE-05.",
   ["Reset.", "Import the file."], "Toast 'Imported exp-test'; every field matches the exported spec.")
tc("PRE-07", "Import a fixture spec", "P1", "Functional", "L", "",
   ["Import qa/fixtures/specs/qa-kitchen-sink.json."], "All components populated (2 HTTP tools, 2 MCP servers, 2 skills, 1 sub-agent, guardrails, hook). Spec is valid.")
tc("PRE-08", "Import wrapped spec {\"spec\": …}", "P2", "Functional", "L", "A JSON file of the form {\"spec\": {...}}.",
   ["Import it."], "The inner spec is imported.")
tc("PRE-09", "Import partial spec fills defaults", "P1", "Functional", "L", "File: {\"name\":\"partial\",\"loop\":{\"maxSteps\":3}}.",
   ["Import it."], "name=partial, maxSteps=3, everything else at defaults; spec valid.")
tc("PRE-10", "Import invalid JSON", "P1", "Negative", "L", "A file containing `{not json`.",
   ["Import it."], "Error toast 'Import failed: …'; current draft untouched.")
tc("PRE-11", "Import non-object JSON", "P2", "Negative", "L", "A file containing `[1,2]`.",
   ["Import it."], "Toast 'Import failed: Expected a JSON object'.")
tc("PRE-12", "Import over an existing draft asks first", "P2", "Functional", "L", "A draft in progress.",
   ["Click Import, cancel the confirm."], "Confirm text 'Importing replaces the current agent. Continue?'; cancel keeps the draft.")
tc("PRE-13", "Copy spec", "P2", "Functional", "L", "", ["Build step → Copy spec, paste into an editor."], "Clipboard holds the full spec JSON; toast 'Copied to clipboard'.")

# =====================================================================
section("IDN", "Identity & API (server)",
        "Run the agent with `run qa-base` unless stated; it listens on 127.0.0.1:8090.")
tc("IDN-01", "Name validation", "P0", "Validation", "L", "",
   ["Try names: `a`, `agent_1-x`, 64 chars, 65 chars, `has space`, `émoji`, empty."],
   "Valid: first three. Invalid (error `name: use 1-64 letters…`): 65 chars, space, non-ASCII, empty.")
tc("IDN-02", "Advertised model id", "P0", "Functional", "B", "run qa-base server.modelId=my-model",
   ["`curl localhost:8090/v1/models`", "`curl localhost:8090/v1/models/my-model`", "`curl localhost:8090/v1/models/other`"],
   "List contains one model `my-model` (owned_by openharness). Single lookup 200. Unknown id → 404 'model not found'. Chat responses carry `\"model\": \"my-model\"`.")
tc("IDN-03", "Client may send any model name", "P1", "Functional", "B", "run qa-base",
   ["send \"hi\" --body '{\"model\":\"gpt-4o\"}'"], "Request succeeds; the configured upstream model is used (mock /requests shows mock-primary).")
tc("IDN-04", "Host and port", "P0", "Functional", "B", "run qa-base server.host=0.0.0.0 server.port=8095",
   ["From another machine on the LAN: `curl http://<ip>:8095/healthz`."], "Reachable on all interfaces at port 8095. With host 127.0.0.1 it is not reachable from other machines.")
tc("IDN-05", "Port validation", "P1", "Validation", "L", "", ["Set port 0, 70000, 65535."], "0 and 70000 → `server.port: must be 1-65535`; 65535 valid.")
tc("IDN-06", "Bearer auth: missing / wrong / right key", "P0", "Security", "B",
   "`export QA_HARNESS_KEYS=k1,k2` then run qa-base server.authMode=bearer",
   ["send \"hi\"", "send \"hi\" --header 'Authorization: Bearer nope'", "send \"hi\" --header 'Authorization: Bearer k2'"],
   "First two → HTTP 401 `invalid API key`. Third → 200. Any key in the comma list works.")
tc("IDN-07", "Bearer auth with env var unset", "P1", "Negative", "B", "`unset QA_HARNESS_KEYS`; run qa-base server.authMode=bearer",
   ["send \"hi\" --header 'Authorization: Bearer x'"], "HTTP 500 `server auth is enabled but QA_HARNESS_KEYS is not set`.")
tc("IDN-08", "Bearer auth requires env var name", "P2", "Validation", "L", "",
   ["Auth mode bearer, clear 'Accepted keys env var'."], "Error `server.apiKeyEnv: required when authMode is bearer`.")
tc("IDN-09", "Auth scope: /healthz open, /v1/* protected", "P1", "Security", "B", "As IDN-06.",
   ["`curl localhost:8090/healthz`", "`curl localhost:8090/v1/models`", "`curl localhost:8090/v1/harness`"],
   "/healthz 200 without a key; /v1/models and /v1/harness 401 without a key.")
tc("IDN-10", "CORS allow-list", "P1", "Functional", "B", "run qa-base 'server.corsOrigins=[\"https://app.example\"]'",
   ["`curl -i -X OPTIONS localhost:8090/v1/chat/completions -H 'Origin: https://app.example'`", "Same with `Origin: https://evil.example`."],
   "Allowed origin: 204 with Access-Control-Allow-Origin echoing it, Allow-Headers incl. Authorization and X-Session-Id, Expose-Headers X-Harness-Trace-Id. Other origin: no ACAO header.")
tc("IDN-11", "CORS wildcard and disabled", "P2", "Functional", "B", "",
   ["Run with corsOrigins=[\"*\"] and an arbitrary Origin.", "Run with corsOrigins=[]."], "`*`: any origin echoed. Empty: no CORS headers at all.")
tc("IDN-12", "Stream tool activity on/off", "P0", "Functional", "B", "",
   ["run qa-base server.streamToolEvents=false; send '[[tool:calculator {\"expression\":\"1+1\"}]]' --stream", "Repeat with streamToolEvents=true."],
   "Off: no `reasoning_content` deltas, content only. On: activity `▸ calculator …` and `✓ 2` arrive as reasoning_content before the answer.")
tc("IDN-13", "Request timeout", "P1", "Functional", "B", "run qa-base server.requestTimeoutSec=3",
   ["send \"[[slow:6]] hi\""], "Request fails after ~3 s (non-stream: HTTP 502 with a deadline/context error).")
tc("IDN-14", "GET /v1/harness strips secrets", "P0", "Security", "B",
   "run qa-base model.apiKey=sk-inline-secret 'model.fallbacks=[{\"provider\":\"openai\",\"baseUrl\":\"http://127.0.0.1:18098/v1\",\"model\":\"b\",\"apiKey\":\"sk-fb-secret\"}]'",
   ["`curl localhost:8090/v1/harness`"], "Spec returned with `apiKey` empty for the model and every fallback; `tools` lists registered tool names.")
tc("IDN-15", "Description shown in spec and models", "P2", "Functional", "B", "", ["Set description, run, GET /v1/harness."], "Description present in the returned spec.")

# =====================================================================
section("MOD", "Model adapter",
        "Fixtures running. `[[echo-params]]` makes the mock reply with what it received.")
tc("MOD-01", "OpenAI-compatible provider basics", "P0", "Functional", "B", "run qa-base",
   ["send \"hello\"", "inspect mock -n 1"], "Reply 'Hello from mock (model=mock-primary)'. Upstream request went to /v1/chat/completions with stream=true and stream_options.include_usage.")
tc("MOD-02", "Anthropic provider basics", "P0", "Functional", "B", "run qa-anthropic",
   ["send \"[[echo-params]]\"", "send '[[tool:calculator {\"expression\":\"2+2\"}]]' --stream"],
   "Upstream path /v1/messages with headers anthropic-version 2023-06-01 and x-api-key. Tool call round-trips: content 'TOOL_RESULT: 4'.")
tc("MOD-03", "Anthropic message conversion", "P1", "Functional", "B", "run qa-anthropic",
   ["send \"[[echo-messages]]\" --system 'extra system'", "Send a history starting with an assistant message (--history file)."],
   "System messages are hoisted into the `system` field; a conversation starting with assistant gets a '(continuing conversation)' user turn first; consecutive same-role turns are merged.")
tc("MOD-04", "API key from env var", "P0", "Security", "B", "`export QA_MODEL_KEY=sk-test-123`; run qa-base model.apiKeyEnv=QA_MODEL_KEY",
   ["send \"[[echo-params]]\""], "Mock sees `Authorization: Bearer sk-test…`. Key is not in the binary (`--print-spec` shows only the env var name).")
tc("MOD-05", "Inline API key", "P1", "Security", "B", "run qa-base model.apiKey=sk-inline-abc model.apiKeyEnv=",
   ["send \"[[echo-params]]\""], "Mock sees `Bearer sk-inline…`. UI shows a warning that inline keys are baked into the binary.")
tc("MOD-06", "No key configured", "P2", "Functional", "B", "run qa-base model.apiKeyEnv=", ["send \"[[echo-params]]\""], "No Authorization header sent (keyless local servers such as Ollama work).")
tc("MOD-07", "Temperature / top_p / max_tokens from spec", "P0", "Functional", "B", "run qa-base model.temperature=0.7 model.topP=0.9 model.maxTokens=321",
   ["send \"[[echo-params]]\""], "temperature 0.7, top_p 0.9, max_tokens 321.")
tc("MOD-08", "Null temperature / topP omitted", "P1", "Functional", "B", "run qa-base model.temperature=null", ["send \"[[echo-params]]\""], "temperature null (not sent upstream).")
tc("MOD-09", "Client params respected", "P0", "Functional", "B", "run qa-base model.clientParamPolicy=respect",
   ["send \"[[echo-params]]\" --body '{\"temperature\":0.1,\"top_p\":0.5,\"max_tokens\":50}'", "send \"[[echo-params]]\" --body '{\"max_tokens\":50,\"max_completion_tokens\":77}'"],
   "First: 0.1 / 0.5 / 50. Second: max_tokens 77 (max_completion_tokens wins).")
tc("MOD-10", "Client params ignored", "P1", "Functional", "B", "run qa-base model.clientParamPolicy=ignore",
   ["send \"[[echo-params]]\" --body '{\"temperature\":0.1,\"max_tokens\":50}'"], "Spec values used (0.3, 1024).")
tc("MOD-11", "Reasoning effort (OpenAI only)", "P2", "Functional", "B", "run qa-base model.reasoningEffort=high",
   ["send \"[[echo-params]]\"", "Repeat on qa-anthropic."], "OpenAI: reasoning_effort 'high' sent. Anthropic: not sent.")
tc("MOD-12", "Extra body fields", "P1", "Functional", "B", "run qa-base 'model.extraBody={\"seed\":7,\"service_tier\":\"flex\"}'",
   ["send \"[[echo-params]]\""], "extra_keys contains seed and service_tier.")
tc("MOD-13", "Extra headers", "P1", "Functional", "B", "run qa-base 'model.extraHeaders={\"X-Org\":\"qa\"}'", ["send \"[[echo-params]]\""], "headers include X-Org: qa.")
tc("MOD-14", "Retries on 5xx with backoff", "P0", "Functional", "B", "run qa-base model.retries=2",
   ["reset; send \"[[fail-once:500]] [[say:ok]]\"", "inspect stats"], "Reply 'ok'; mock-primary received 2 requests (1 failure + 1 retry); warning logged 'model call failed'.")
tc("MOD-15", "Retries on 429", "P1", "Functional", "B", "As MOD-14.", ["send \"[[fail-once:429]] [[say:ok]]\""], "Reply 'ok' after one retry.")
tc("MOD-16", "No retry on 4xx (except 429)", "P0", "Functional", "B", "run qa-base model.retries=3",
   ["reset; send \"[[status:400]]\"", "inspect stats"], "HTTP 502 'model API returned 400…'; exactly 1 upstream request.")
tc("MOD-17", "Retries exhausted", "P1", "Negative", "B", "run qa-base model.retries=2", ["reset; send \"[[status:500]]\"", "inspect stats"],
   "HTTP 502; 3 upstream requests (1 + 2 retries), spaced by increasing backoff (~0.5 s, ~2 s).")
tc("MOD-18", "Fallback chain", "P0", "Functional", "B",
   "run qa-base model.model=fail-500-primary model.retries=1 'model.fallbacks=[{\"provider\":\"openai\",\"baseUrl\":\"http://127.0.0.1:18098/v1\",\"model\":\"backup-model\",\"apiKeyEnv\":\"\"}]'",
   ["reset; send \"[[echo-params]]\"", "inspect stats", "inspect trace -n 3"],
   "Answer comes from backup-model; stats: fail-500-primary 2, backup-model 1; trace llm/response model=backup-model.")
tc("MOD-19", "Fallback on 4xx", "P1", "Functional", "B", "As MOD-18 with model.model=fail-401-primary.", ["send \"hi\""], "No retries on the 401 primary (1 request), then the fallback answers.")
tc("MOD-20", "Cross-provider fallback", "P1", "Functional", "B",
   "Primary openai fail-500-x; fallback {provider: anthropic, baseUrl: http://127.0.0.1:18098, model: mock-claude}.",
   ["send \"hello\""], "Anthropic fallback answers 'Hello from mock (model=mock-claude)'.")
tc("MOD-21", "Model call timeout", "P1", "Functional", "B", "run qa-base model.timeoutSec=2 model.retries=0", ["send \"[[slow:5]] hi\""], "Fails after ~2 s with HTTP 502 (client timeout).")
tc("MOD-22", "finish_reason length passes through", "P2", "Functional", "B", "run qa-base", ["send \"[[length]]\""], "finish_reason `length`.")
tc("MOD-23", "Provider / required field validation", "P0", "Validation", "L", "",
   ["Clear model name; clear base URL; set a fallback with empty model."],
   "Errors: `model.model: required`, `model.baseUrl: required`, `model.fallbacks[0].model: required`.")
tc("MOD-24", "Quick-fill buttons", "P2", "UI", "L", "", ["Click DeepSeek, OpenAI, Anthropic, Ollama, OpenRouter quick-fills."],
   "Provider, base URL, model and key env var are filled with that vendor's values.")
tc("MOD-25", "JSON fields reject invalid JSON", "P2", "Validation", "L", "", ["Type `{bad` into Extra body."], "Inline JSON error; spec not updated until valid.")

# =====================================================================
section("PRM", "System prompt assembly", "Use `[[echo-system]]` to see the exact system prompt.")
tc("PRM-01", "Base system prompt", "P0", "Functional", "B", "run qa-base prompt.includeDateTime=false",
   ["send \"[[echo-system]]\""], "Reply starts with 'You are the QA agent.'")
tc("PRM-02", "Sections: enabled, disabled, order", "P0", "Functional", "B", "run qa-kitchen-sink (Style enabled, 'Disabled section' disabled)",
   ["send \"[[echo-system]]\" --session prm2"], "Contains '## Style\\nAnswer in short sentences.'; does NOT contain 'THIS MUST NOT APPEAR'. Reordering in the UI changes the order.")
tc("PRM-03", "Date and time line", "P1", "Functional", "B", "", ["includeDateTime on, then off; send \"[[echo-system]]\"."], "On: 'Current date and time: <weekday, date time zone>'. Off: absent.")
tc("PRM-04", "Tool summary", "P1", "Functional", "B", "run qa-base prompt.includeToolSummary=true", ["send \"[[echo-system]]\""],
   "'## Available tools' lists current_time, calculator, todo with descriptions. With loop.strategy=single_shot the list is absent.")
tc("PRM-05", "Client system policy: append", "P0", "Functional", "B", "run qa-base prompt.clientSystemPolicy=append",
   ["send \"[[echo-system]]\" --system 'CLIENT-SYS'"], "Base prompt kept; ends with '## Additional instructions from the caller\\nCLIENT-SYS'.")
tc("PRM-06", "Client system policy: replace", "P0", "Functional", "B", "prompt.clientSystemPolicy=replace",
   ["send \"[[echo-system]]\" --system 'CLIENT-SYS'", "send \"[[echo-system]]\" (no system)"],
   "With a client system message: base prompt replaced by 'CLIENT-SYS' (sections etc. still appended). Without: base prompt used.")
tc("PRM-07", "Client system policy: ignore", "P1", "Functional", "B", "prompt.clientSystemPolicy=ignore", ["send \"[[echo-system]]\" --system 'CLIENT-SYS'"], "'CLIENT-SYS' absent.")
tc("PRM-08", "Developer role treated as system", "P2", "Functional", "B", "policy append",
   ["Send messages [{role: developer, content: DEV-MSG}, {role: user, content: [[echo-system]]}] via --history."], "DEV-MSG appears under 'Additional instructions from the caller'.")
tc("PRM-09", "Assembled prompt preview in UI", "P2", "UI", "L", "", ["Edit system prompt, sections, notes, skills; watch the preview."], "Preview reflects edits live in the documented order.")
tc("PRM-10", "Section order in full prompt", "P1", "Functional", "B", "run qa-kitchen-sink", ["send \"[[echo-system]]\" --session prm10"],
   "Order: base → sections → date → Available tools → Skills → Notes → What you remember → memory tool hint → Context from hooks → caller instructions.")

# =====================================================================
section("TLB", "Built-in tools", "run qa-kitchen-sink (all built-ins, files writable, shell allows ls/echo/cat, fetch allows 127.0.0.1). Use a new --session per case.")
tc("TLB-01", "current_time default and time zone", "P1", "Functional", "B", "",
   ["send '[[tool:current_time {}]]'", "send '[[tool:current_time {\"timezone\":\"Asia/Tokyo\"}]]'", "send '[[tool:current_time {\"timezone\":\"Mars/Base\"}]]'"],
   "UTC time; Tokyo time with JST; third → 'Error: unknown time zone \"Mars/Base\"'.")
tc("TLB-02", "calculator correctness", "P0", "Functional", "B", "",
   ["Expressions: `2+3*4`, `(2+3)*4`, `2^3^2`, `2**10`, `10 % 4`, `sqrt(16)+abs(-2)`, `1e3/4`, `pi`, `round(2.5)`, `0.1+0.2`"],
   "14, 20, 512, 1024, 2, 6, 250, 3.14159265358979, 3, 0.3 (15 significant digits).")
tc("TLB-03", "calculator errors", "P1", "Negative", "B", "",
   ["Expressions: `1/0`, `2+`, `foo(1)`, `sqrt 4`, `2)`"], "Errors: division by zero; unexpected end; unknown function \"foo\"; sqrt needs parentheses; unexpected ')' — returned as tool errors, agent keeps running.")
tc("TLB-04", "http_fetch HTML to text", "P0", "Functional", "B", "",
   ["send '[[tool:http_fetch {\"url\":\"http://127.0.0.1:18099/page\"}]]'"],
   "Result starts 'HTTP 200 http://127.0.0.1:18099/page' and contains 'The magic number is 4242.', 'alpha', 'beta'; does NOT contain 'should-not-appear' or CSS.")
tc("TLB-05", "http_fetch domain allow-list", "P0", "Security", "B", "fetch.allowedDomains=[\"127.0.0.1\"]",
   ["Fetch http://localhost:18099/page", "Set allowedDomains=[\"*.example.com\"], fetch https://sub.example.com/ and https://example.org/"],
   "localhost → 'domain \"localhost\" is not in the allowlist'. Subdomains of example.com allowed; example.org rejected. Empty list = any domain.")
tc("TLB-06", "http_fetch scheme restriction", "P1", "Security", "B", "", ["Fetch `file:///etc/passwd` and `ftp://x`."], "'only http(s) URLs are allowed'.")
tc("TLB-07", "http_fetch maxBytes", "P1", "Functional", "B", "tools.fetch.maxBytes=1000", ["Fetch http://127.0.0.1:18099/big"], "Body cut to 1000 bytes (no error).")
tc("TLB-08", "read_file / list_dir", "P0", "Functional", "B", "Create qa-data/workspace/docs/a.txt with 5 lines.",
   ["list_dir {}", "list_dir {\"path\":\"docs\"}", "read_file {\"path\":\"docs/a.txt\"}", "read_file with offset 2, limit 2", "list_dir on an empty folder"],
   "Directories end with '/', files show '(N bytes)'; full file; lines 2–3 only; '(empty directory)'.")
tc("TLB-09", "write_file creates folders", "P0", "Functional", "B", "files.readOnly=false",
   ["send '[[tool:write_file {\"path\":\"new/dir/x.txt\",\"content\":\"hi\"}]]'"], "'wrote 2 bytes to new/dir/x.txt'; file exists under qa-data/workspace/new/dir/.")
tc("TLB-10", "write_file read-only workspace", "P0", "Security", "B", "tools.files.readOnly=true", ["write_file as in TLB-09."], "'Error: the workspace is read-only'; nothing written.")
tc("TLB-11", "Workspace escape blocked", "P0", "Security", "B", "",
   ["read_file ../../etc/hosts", "read_file /etc/hosts", "write_file ../outside.txt", "Create a symlink qa-data/workspace/link → /etc, read_file link/hosts"],
   "Every attempt → 'path … is outside the workspace'; no file outside qa-data/workspace is read or created.")
tc("TLB-12", "shell allow-list", "P0", "Security", "B", "shell.allowedCommands=[ls,echo,cat]",
   ["shell {command: echo, args: [hi there]}", "shell {command: rm, args: [-rf, x]}", "shell {command: /bin/ls}", "shell {command: \"echo; rm\"}"],
   "'hi there'; rm → not in the allowlist (lists allowed); path → 'command must be a bare program name'; injection attempt → not allowed (no shell interpretation).")
tc("TLB-13", "shell runs in workspace, no shell expansion", "P1", "Security", "B", "", ["shell {command: echo, args: [\"$HOME\", \"*\"]}", "shell {command: ls}"],
   "Literal '$HOME *' echoed (no expansion); ls lists the workspace root.")
tc("TLB-14", "shell exit codes and empty output", "P1", "Functional", "B", "", ["shell {command: cat, args: [missing.txt]}", "shell {command: ls, args: [empty-dir]}"],
   "Error text plus '[exit: exit status 1]'; empty output → '(no output, exit 0)'.")
tc("TLB-15", "shell timeout", "P1", "Functional", "B", "allowedCommands=[sleep], shell.timeoutSec=2", ["shell {command: sleep, args: [5]}"], "Killed after ~2 s with an exit error.")
tc("TLB-16", "shell empty allow-list means any command", "P2", "Security", "B", "allowedCommands=[]", ["shell {command: whoami}"], "Runs (if permission allows). UI warns this is high risk.")
tc("TLB-17", "todo set / done / list", "P1", "Functional", "B", "",
   ["send '[[tool:todo {\"action\":\"set\",\"items\":[\"a\",\"b\"]}]] [[tool:todo {\"action\":\"done\",\"indexes\":[2]}]]' with tools.parallel=false", "Then action list / action foo"],
   "'1. [ ] a\\n2. [x] b'; unknown action → error. The list resets every request.")
tc("TLB-18", "Unknown built-in rejected", "P1", "Validation", "L", "Import a spec with tools.builtins=[\"teleport\"].", ["Validate."], "`tools.builtins: unknown tool \"teleport\"`.")
tc("TLB-19", "Disabled built-ins are not offered", "P0", "Functional", "B", "run qa-base (current_time, calculator, todo)",
   ["send '[[tool:shell {\"command\":\"ls\"}]]'", "inspect mock -n 1"], "Upstream request lists 3 tools; calling shell → 'Error: unknown tool \"shell\"'.")

# =====================================================================
section("TLH", "HTTP tools", "run qa-kitchen-sink with `export QA_TOOL_TOKEN=tok123`. qa_lookup = GET, qa_create = POST.")
tc("TLH-01", "GET tool: URL placeholders and query args", "P0", "Functional", "B", "",
   ["send '[[tool:qa_lookup {\"id\":\"A 1\",\"verbose\":true}]]'"],
   "Webhook receives GET /tool?id=A+1&verbose=true (placeholder URL-encoded, remaining args as query).")
tc("TLH-02", "POST tool: JSON body", "P0", "Functional", "B", "", ["send '[[tool:qa_create {\"title\":\"hi\"}]]'"], "POST with JSON body {\"title\":\"hi\"} and Content-Type application/json.")
tc("TLH-03", "Header env expansion keeps secrets out of the spec", "P0", "Security", "B", "",
   ["Run both tools; inspect webhook.", "`--print-spec` on the binary."], "Webhook sees X-QA: tok123 and Authorization: Bearer tok123; the spec only contains `${QA_TOOL_TOKEN}`.")
tc("TLH-04", "Upstream error status", "P1", "Negative", "B", "Change qa_lookup URL to …/tool?status=404&id={{id}}", ["Call it."], "Tool error 'HTTP 404: …'; the model sees it and the turn continues.")
tc("TLH-05", "Upstream unreachable", "P1", "Negative", "B", "URL http://127.0.0.1:1/x", ["Call the tool."], "Tool error (connection refused); no crash.")
tc("TLH-06", "Tool timeout", "P1", "Functional", "B", "tools.timeoutSec=2; URL …/tool?delay=5000", ["Call it."], "Tool error after ~2 s (context deadline).")
tc("TLH-07", "Default method and parameters", "P2", "Functional", "B", "HTTP tool with method empty and parameters empty.", ["Call it with {}."], "POST is used; tool offered with an empty object schema.")
tc("TLH-08", "Name validation and duplicates", "P1", "Validation", "L", "",
   ["Add tools named `bad name`, two named `dup`, one with empty URL, one with parameters `{bad`."],
   "Errors: invalid name; duplicate \"dup\"; url required; parameters not valid JSON.")
tc("TLH-09", "Large response truncation", "P1", "Functional", "B", "tools.resultMaxChars=500, URL …/tool?big=5000", ["Call it."], "Result truncated to ~500 chars with '…[N chars truncated]…' (head+tail).")

# =====================================================================
section("TLM", "MCP servers", "run qa-kitchen-sink: `qastdio` (stdio, qa/fixtures/mcp_stdio_server.py) and `qahttp` (http, webhook /mcp).")
tc("TLM-01", "Both servers connect and list tools", "P0", "Functional", "B", "",
   ["Read the agent log.", "`curl localhost:8090/v1/harness`"],
   "Log: 'MCP server connected server=qastdio tools=5' and 'qahttp tools=3'. Tools mcp_qastdio_{echo,add,env,fail,slow}, mcp_qahttp_{echo,add,fail}.")
tc("TLM-02", "stdio tool call", "P0", "Functional", "B", "", ["send '[[tool:mcp_qastdio_add {\"a\":2,\"b\":3}]]'"], "TOOL_RESULT: 5.")
tc("TLM-03", "stdio env injection", "P1", "Functional", "B", "", ["send '[[tool:mcp_qastdio_env {\"name\":\"MCP_QA_VAR\"}]]'"], "'hello-from-spec'.")
tc("TLM-04", "MCP isError result", "P1", "Negative", "B", "", ["Call mcp_qastdio_fail and mcp_qahttp_fail."], "Tool error 'deliberate MCP failure'; turn continues.")
tc("TLM-05", "HTTP transport (JSON and SSE replies)", "P0", "Functional", "B", "",
   ["Call mcp_qahttp_add {a:1,b:2}.", "Change qahttp URL to …/mcp?sse=1, restart, repeat."], "'3' both times; Mcp-Session-Id is echoed on later requests (webhook log).")
tc("TLM-06", "Unavailable server does not block start-up", "P0", "Negative", "B", "Add stdio server with command `does-not-exist` and an http server at http://127.0.0.1:1/mcp.",
   ["Start the agent.", "send \"hi\""], "Warnings 'MCP server unavailable; continuing without it'; agent serves requests; other tools work.")
tc("TLM-07", "MCP tool timeout", "P1", "Functional", "B", "tools.timeoutSec=2", ["send '[[tool:mcp_qastdio_slow {\"seconds\":5}]]'"], "Tool error after ~2 s; the agent keeps running and the server stays usable.")
tc("TLM-08", "Permission group mcp:<server>", "P0", "Security", "B", "permissions.toolPolicies={\"mcp:qastdio\":\"deny\"}",
   ["Call mcp_qastdio_echo and mcp_qahttp_echo."], "qastdio → 'permission denied … denied by policy'; qahttp allowed. An exact tool-name policy overrides the group.")
tc("TLM-09", "MCP validation", "P1", "Validation", "L", "", ["stdio without command; http without URL; name with spaces; transport 'ws'."],
   "Errors: command required for stdio; url required for http; invalid name; transport must be stdio or http.")
tc("TLM-10", "MCP processes cleaned up", "P1", "Resilience", "B", "", ["Start the agent, Ctrl-C it.", "`ps aux | grep mcp_stdio_server`"], "No orphaned MCP server processes remain.")
tc("TLM-11", "Long tool names truncated to 64 chars", "P2", "Functional", "B", "MCP server named with 40 chars exposing a 40-char tool.",
   ["List tools."], "Exposed name is `mcp_<server>_<tool>` cut to 64 chars, invalid characters replaced with '_'.")

# =====================================================================
section("TLP", "Tool execution pipeline", "run qa-base unless stated.")
tc("TLP-01", "Parallel tool calls", "P0", "Functional", "B", "tools.parallel=true maxParallel=4; add mcp qastdio",
   ["send '[[tool:mcp_qastdio_slow {\"seconds\":2}]] [[tool:mcp_qastdio_slow {\"seconds\":2}]] [[tool:mcp_qastdio_slow {\"seconds\":2}]]'"],
   "Finishes in ~2 s (concurrent). Results returned in call order.")
tc("TLP-02", "maxParallel cap", "P1", "Functional", "B", "As TLP-01 with maxParallel=1", ["Same request."], "~6 s (one at a time).")
tc("TLP-03", "Sequential execution preserves order", "P0", "Functional", "B", "qa-kitchen-sink tools.parallel=false",
   ["send '[[tool:write_file {\"path\":\"o.txt\",\"content\":\"v1\"}]] [[tool:read_file {\"path\":\"o.txt\"}]]'"],
   "'wrote 2 bytes … || v1'. NOTE: with parallel=true the read may run before the write (expected for concurrent calls).")
tc("TLP-04", "Result truncation head_tail vs head", "P1", "Functional", "B", "qa-kitchen-sink tools.resultMaxChars=200",
   ["Fetch http://127.0.0.1:18099/big with resultTruncation=head_tail, then head."],
   "head_tail: start + '…[N chars truncated]…' + end. head: start + '…[N chars truncated]'.")
tc("TLP-05", "Client tool passthrough", "P0", "Functional", "B", "tools.clientToolsPolicy=passthrough",
   ["send '[[tool:get_weather {\"city\":\"Paris\"}]]' --body '{\"tools\":[{\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"parameters\":{\"type\":\"object\"}}}]}'"],
   "finish_reason tool_calls; tool_calls[0].function.name get_weather with arguments {\"city\": \"Paris\"}.")
tc("TLP-06", "Client tool round trip", "P0", "Functional", "B", "As TLP-05.",
   ["Send history: user(directive) → assistant(tool_calls from TLP-05) → tool(tool_call_id, '18C sunny')."], "Final answer 'TOOL_RESULT: 18C sunny'.")
tc("TLP-07", "Client tools ignored", "P1", "Functional", "B", "tools.clientToolsPolicy=ignore", ["Same as TLP-05.", "inspect mock -n 1"], "get_weather not sent upstream; call → 'unknown tool'.")
tc("TLP-08", "Internal tool wins name collision", "P2", "Functional", "B", "", ["Pass a client tool named `calculator`."], "Only the built-in calculator is offered and executed server-side.")
tc("TLP-09", "tool_choice none / required / named", "P1", "Functional", "B", "",
   ["send \"[[echo-params]]\" --body '{\"tool_choice\":\"none\"}'", "Same with \"required\" and {\"type\":\"function\",\"function\":{\"name\":\"calculator\"}}"],
   "none: tools list empty. required/named: tool_choice forwarded (Anthropic: mapped to any / tool).")
tc("TLP-10", "Invalid tool-call arguments", "P1", "Negative", "B", "", ["send '[[tool:calculator not-json]]'"], "Tool receives {\"_raw\":…} → error 'invalid arguments' or calculator error; turn continues.")
tc("TLP-11", "Unknown tool requested by model", "P1", "Negative", "B", "", ["send '[[tool:nope {}]]'"], "'Error: unknown tool \"nope\"'; turn continues.")
tc("TLP-12", "Activity lines for tools", "P2", "UI", "B", "streamToolEvents=true", ["Successful and failing tool calls with --stream."],
   "Activity shows '▸ name args' then '  ✓ preview' or '  ✗ error'. Long args/results shortened.")

# =====================================================================
section("SKL", "Skills")
tc("SKL-01", "Progressive: index in prompt, body on demand", "P0", "Functional", "B", "run qa-kitchen-sink",
   ["send \"[[echo-system]]\" --session s1", "send '[[tool:load_skill {\"name\":\"refunds\"}]]' --session s2"],
   "Prompt has '## Skills' listing refunds and shipping with descriptions but NOT 'SKILL-REFUNDS-BODY'. load_skill returns '# Skill: refunds' + body.")
tc("SKL-02", "load_skill enum and unknown skill", "P1", "Negative", "B", "", ["inspect mock (tool schema)", "send '[[tool:load_skill {\"name\":\"nope\"}]]'"],
   "Schema enum = [refunds, shipping]; unknown → 'unknown skill \"nope\"'.")
tc("SKL-03", "Eager mode", "P0", "Functional", "B", "qa-kitchen-sink skills.mode=eager", ["send \"[[echo-system]]\"", "List tools."],
   "Prompt contains '## Skill: refunds' with full body; load_skill tool absent.")
tc("SKL-04", "No skills → no tool, no section", "P1", "Functional", "B", "run qa-base", ["[[echo-system]]; list tools."], "No Skills section, no load_skill.")
tc("SKL-05", "Skill name validation", "P2", "Validation", "L", "", ["Skill named `bad name`."], "`skills.items[0].name: invalid`.")
tc("SKL-06", "Skills via UI editor", "P2", "UI", "L", "", ["Add, edit, reorder and delete skills."], "Changes reflected in spec JSON and the prompt preview.")

# =====================================================================
section("MEM", "Memory", "run qa-kitchen-sink (persistent, per_user, autoInject 5, tools on, static note). `reset` between cases.")
tc("MEM-01", "Static notes injected", "P0", "Functional", "B", "", ["send \"[[echo-system]]\""], "'## Notes\\nQA-STATIC-NOTE: the customer prefers email.'")
tc("MEM-02", "memory_save persists to disk", "P0", "Functional", "B", "",
   ["send '[[tool:memory_save {\"text\":\"likes tea\"}]]' --user alice", "cat qa-data/memory.json"],
   "'saved as m…'; file has an entry {scope: alice, text: likes tea}. File mode 0600.")
tc("MEM-03", "Auto-inject recent memories", "P0", "Functional", "B", "After MEM-02.", ["send \"[[echo-system]]\" --user alice"], "'## What you remember' lists '[m…] likes tea'.")
tc("MEM-04", "Per-user isolation", "P0", "Security", "B", "After MEM-02.", ["send \"[[echo-system]]\" --user bob", "send '[[tool:memory_search {\"query\":\"tea\"}]]' --user bob"],
   "Bob sees no 'likes tea' and search returns '(no matching memories)'.")
tc("MEM-05", "Scope key fallback order", "P1", "Functional", "B", "", ["Save with no user but X-Session-Id s9; then search with --session s9; then with neither."],
   "Scope = user field, else session id, else 'default'.")
tc("MEM-06", "Global scope", "P1", "Functional", "B", "memory.scope=global", ["Save as alice, search as bob."], "Bob finds alice's memory.")
tc("MEM-07", "memory_search ranking and limit", "P2", "Functional", "B", "Save 'tea green', 'tea black', 'coffee'.",
   ["search {query: 'tea green'}", "search {query: '', limit: 2}"], "Best overlap first; empty query → 2 most recent.")
tc("MEM-08", "memory_delete", "P1", "Functional", "B", "", ["Delete an existing id, then the same id again, then another user's id."], "'deleted'; then 'no memory with id …' (other users' ids can't be deleted).")
tc("MEM-09", "autoInject=0 and tools off", "P1", "Functional", "B", "memory.autoInject=0 memory.enableTools=false", ["[[echo-system]]; list tools."],
   "No 'What you remember', no memory hint, no memory_* tools.")
tc("MEM-10", "Persistent off", "P1", "Functional", "B", "memory.persistent=false", ["List tools; [[echo-system]]."], "No memory tools or injection; static notes still present.")
tc("MEM-11", "Survives restart", "P0", "Functional", "B", "After MEM-02.", ["Restart the agent; [[echo-system]] as alice."], "Memory still injected.")
tc("MEM-12", "Corrupt memory file", "P2", "Negative", "B", "Write `{oops` to qa-data/memory.json.", ["Start the agent."], "Start-up fails with 'memory file … is corrupt'.")
tc("MEM-13", "Concurrent saves", "P2", "Resilience", "B", "", ["Fire 20 parallel memory_save requests (different texts)."], "All 20 entries present; file valid JSON.")

# =====================================================================
section("SES", "Sessions")
tc("SES-01", "Stateless mode ignores history", "P0", "Functional", "B", "run qa-base", ["send \"[[echo-messages]]\" --session a twice."], "Both replies 'MESSAGES 2' (system + user). No files in qa-data/sessions.")
tc("SES-02", "Server mode with latest-message clients", "P0", "Functional", "B", "run qa-base sessions.mode=server",
   ["send \"one\" --session s1", "send \"[[echo-messages]]\" --session s1"], "Second request upstream has system, user(one), assistant, user. qa-data/sessions/s1.jsonl exists.")
tc("SES-03", "Server mode with full-transcript clients", "P0", "Functional", "B", "As SES-02.",
   ["Turn 1: send \"first\" --session s2.", "Turn 2: send the full transcript [user first, assistant <reply>, user [[echo-messages]]]."],
   "No duplication: upstream sees exactly one 'first' user turn.")
tc("SES-04", "Repeated identical message is not dropped", "P0", "Regression", "B", "As SES-02.", ["send \"continue\" --session s3", "send \"continue\" --session s3", "inspect mock -n 1"],
   "Second upstream request has 4 messages (system, user, assistant, user): the repeated 'continue' is appended, not mistaken for a re-sent transcript.")
tc("SES-05", "Session isolation", "P0", "Security", "B", "As SES-02.", ["Session A says 'secret A'; session B asks [[echo-messages]]."], "B's upstream history has no A messages.")
tc("SES-06", "Custom header name", "P1", "Functional", "B", "sessions.headerName=X-Conversation", ["Send with --header 'X-Conversation: c1' twice."], "History kept for c1; X-Session-Id is ignored.")
tc("SES-07", "`user` field as session fallback", "P1", "Functional", "B", "Server mode.", ["Send twice with --user u1 and no header."], "History persisted under u1.")
tc("SES-08", "maxMessages trimming", "P1", "Functional", "B", "sessions.maxMessages=4", ["Hold a 5-turn conversation; then [[echo-messages]]."],
   "At most 4 stored messages loaded; history never starts with an orphan tool message.")
tc("SES-09", "Tool calls persisted in session", "P1", "Functional", "B", "Server mode.", ["Turn with a calculator call, then a follow-up."], "JSONL holds the assistant tool_calls and tool result; follow-up upstream includes them.")
tc("SES-10", "Unsafe session ids are hashed", "P1", "Security", "B", "Server mode.", ["--session '../../etc/x'"], "File created inside the sessions dir with a hashed name; nothing written outside.")
tc("SES-11", "Survives restart", "P1", "Functional", "B", "Server mode.", ["Two turns, restart, third turn with [[echo-messages]]."], "History intact.")

# =====================================================================
section("CTX", "Context management")
tc("CTX-01", "Sliding window compaction", "P0", "Functional", "B", "run qa-base context.maxContextTokens=60 context.keepRecentMessages=2",
   ["Send a history of 8 long messages (60 words each) + '[[echo-messages]]' --stream."],
   "Activity 'Compacted context (sliding_window): N → 4 messages'. Upstream: system, '[K earlier messages were removed…]' note, last 2 messages.")
tc("CTX-02", "Summarize compaction", "P0", "Functional", "B", "As CTX-01 with context.strategy=summarize",
   ["Same request."], "Extra upstream summary call; note 'Summary of the earlier conversation:\\nSUMMARY OF …'. Trace event 'compaction'.")
tc("CTX-03", "Summary failure falls back to sliding window", "P1", "Negative", "B", "As CTX-02.",
   ["Same long history, but put [[fail-summary]] in the FIRST message (the summary call then gets HTTP 500)."], "Warning 'summarize compaction failed; falling back…'; turn still answers with the sliding-window note.")
tc("CTX-04", "Strategy none", "P1", "Functional", "B", "context.strategy=none, tiny maxContextTokens", ["Same request as CTX-01."], "No compaction; all messages sent.")
tc("CTX-05", "Trigger ratio", "P1", "Functional", "B", "maxContextTokens=1000", ["Find a history size that compacts at triggerRatio=0.5 but not at 0.9."], "Compaction triggers only above ratio × budget.")
tc("CTX-06", "Never cuts between a tool call and its result", "P0", "Functional", "B", "keepRecentMessages=2", ["History ending … assistant(tool_calls), tool, tool, user."],
   "Kept window never starts with a 'tool' message.")
tc("CTX-07", "Tool-result pruning", "P1", "Functional", "B", "pruneToolResults=true pruneKeepLast=1 pruneMaxChars=100",
   ["Multi-step turn fetching /big twice, then echo-messages."], "Older tool result shortened to ~100 chars; newest kept intact.")
tc("CTX-08", "Pruning off", "P2", "Functional", "B", "pruneToolResults=false", ["Same as CTX-07."], "Tool results unchanged.")
tc("CTX-09", "Validation", "P2", "Validation", "L", "", ["triggerRatio 0 and 1.5; strategy 'magic'."], "`context.triggerRatio: must be in (0, 1]`; strategy must be one of none, sliding_window, summarize.")
tc("CTX-10", "Compaction with Anthropic", "P1", "Functional", "B", "qa-anthropic + CTX-01 settings", ["Same request."], "Works; window starting with assistant is fixed up for Anthropic.")

# =====================================================================
section("LOOP", "Agent loop")
tc("LOOP-01", "ReAct multi-step", "P0", "Functional", "B", "run qa-base", ["send '[[tool:calculator {\"expression\":\"6*7\"}]]' --stream"], "Model → tool → model; answer 'TOOL_RESULT: 42'; usage is the sum of both calls.")
tc("LOOP-02", "Plan then execute", "P0", "Functional", "B", "loop.strategy=plan_execute", ["send '[[tool:calculator {\"expression\":\"1+1\"}]]' --stream", "inspect mock -n 3"],
   "Activity 'Plan:\\n1. Step one\\n2. Step two'. First upstream call has no tools; later calls carry the plan as a system message right before the latest user message.")
tc("LOOP-03", "Custom planning prompt", "P2", "Functional", "B", "loop.planningPrompt='Please write a short numbered plan, QA.'", ["As LOOP-02."], "Mock receives the custom prompt.")
tc("LOOP-04", "Single shot", "P0", "Functional", "B", "loop.strategy=single_shot", ["send '[[tool:calculator {}]]'", "inspect mock -n 1"],
   "One upstream call, no internal tools offered (client tools still pass through).")
tc("LOOP-05", "maxSteps then summarize", "P0", "Functional", "B", "loop.maxSteps=3 loop.onMaxSteps=summarize", ["send \"[[loop]]\" --stream"],
   "Activity 'Step limit reached…'; answer 'FINAL AFTER STEP LIMIT'; 4 upstream calls (3 + final, final with no tools).")
tc("LOOP-06", "maxSteps then error", "P0", "Functional", "B", "loop.maxSteps=3 loop.onMaxSteps=error", ["send \"[[loop]]\""], "HTTP 502 'agent did not finish within 3 steps'; on_error hook fires.")
tc("LOOP-07", "maxToolCalls budget", "P1", "Functional", "B", "loop.maxSteps=20 loop.maxToolCalls=3", ["send \"[[loop]]\""],
   "Calls beyond 3 get 'tool call budget for this request is exhausted'; the turn ends with the step-limit answer.")
tc("LOOP-08", "Time budget", "P1", "Functional", "B", "loop.timeBudgetSec=2 loop.maxSteps=20", ["send \"[[loop]] [[slow:1]]\" --stream"], "After ~2 s of steps: activity 'Time budget exhausted', then 'FINAL AFTER STEP LIMIT'.")
tc("LOOP-09", "onToolError continue", "P1", "Functional", "B", "loop.onToolError=continue", ["send '[[tool:calculator {\"expression\":\"1/0\"}]]'"], "'TOOL_RESULT: Error: division by zero' — the model saw the error.")
tc("LOOP-10", "onToolError stop", "P0", "Functional", "B", "loop.onToolError=stop", ["Same request."], "Answer 'I stopped because a tool failed: division by zero'; no further model call.")
tc("LOOP-11", "Self-check approves", "P0", "Functional", "B", "loop.selfCheck.enabled=true", ["send \"hello\" --stream"], "Activity 'Self-check: approved'; answer unchanged; one extra upstream call.")
tc("LOOP-12", "Self-check revises up to maxRevisions", "P0", "Functional", "B", "selfCheck.enabled=true maxRevisions=2", ["send \"[[selfcheck:revise]] draft\" --stream"],
   "Two 'Self-check: revised the answer' lines; final 'REVISED ANSWER'.")
tc("LOOP-13", "Self-check buffers streaming", "P1", "Functional", "B", "selfCheck on", ["send \"hello\" --stream --raw"], "Content arrives in one chunk after the self-check (no draft tokens leak).")
tc("LOOP-14", "Live streaming when nothing buffers", "P1", "Functional", "B", "selfCheck off, no output rules, redactPII off", ["send \"[[say:a long answer …]]\" --stream --raw"], "Content arrives in multiple chunks as the model streams.")
tc("LOOP-15", "Validation", "P1", "Validation", "L", "", ["maxSteps 0; strategy 'tree'; onToolError 'retry'; onMaxSteps 'ignore'."], "Each rejected with the allowed values.")
tc("LOOP-16", "Loop diagram reflects settings", "P2", "UI", "L", "", ["Toggle strategy, self-check, guardrails, hooks; watch the lifecycle diagram."], "Planning node, self-check, guardrail counts and hook counts update.")

# =====================================================================
section("SUB", "Sub-agents", "run qa-kitchen-sink (sub-agent `mathwhiz`, tools [calculator], maxSteps 4).")
tc("SUB-01", "Delegation works", "P0", "Functional", "B", "", ["send \"[[subagent:mathwhiz]]\" --stream"],
   "'TOOL_RESULT: TOOL_RESULT: 256'. Activity shows nested lines prefixed '  │ ' for the sub-agent's calculator call.")
tc("SUB-02", "Sub-agent tool scoping", "P0", "Security", "B", "", ["send \"[[subagent:mathwhiz current_time]]\""], "Sub-agent gets 'unknown tool \"current_time\"'.")
tc("SUB-03", "Fresh context", "P1", "Functional", "B", "", ["Check the sub-agent's upstream request (inspect mock)."], "Only its own system prompt and the task; none of the parent conversation.")
tc("SUB-04", "Model override", "P1", "Functional", "B", "subagents[0].model=mock-sub", ["SUB-01 then inspect stats."], "Sub-agent calls use mock-sub; parent uses mock-primary.")
tc("SUB-05", "Sub-agent maxSteps and default", "P1", "Functional", "B", "maxSteps=1; then 0", ["Delegate a task needing 2 steps."], "1 → step-limit summary inside the sub-agent. 0 → default 6 steps.")
tc("SUB-06", "Nesting limit", "P2", "Negative", "B", "Sub-agent A may call agent_B, B may call agent_A.", ["Trigger recursive delegation."], "At depth 3: 'sub-agent nesting limit reached'.")
tc("SUB-07", "Usage includes sub-agent calls", "P1", "Functional", "B", "", ["SUB-01 non-stream."], "usage.total_tokens counts parent and sub-agent calls (18 per mock call).")
tc("SUB-08", "Validation", "P1", "Validation", "L", "", ["Sub-agent with invalid name; empty system prompt."], "Errors `subagents[0].name: invalid`, `subagents[0].systemPrompt: required`.")
tc("SUB-09", "Sub-agent permissions still apply", "P1", "Security", "B", "permissions.toolPolicies={calculator: deny}", ["SUB-01."], "Sub-agent's calculator call is denied by policy.")

# =====================================================================
section("PERM", "Permissions and sandbox")
tc("PERM-01", "Default allow", "P0", "Functional", "B", "run qa-base permissions.defaultPolicy=allow", ["Call calculator."], "Runs.")
tc("PERM-02", "Default deny", "P0", "Security", "B", "permissions.defaultPolicy=deny", ["Call calculator."], "'permission denied for calculator: denied by policy'; trace event tool/denied.")
tc("PERM-03", "Per-tool override beats default", "P0", "Security", "B", "defaultPolicy=deny, toolPolicies={calculator: allow}", ["Call calculator and current_time."], "calculator runs; current_time denied.")
tc("PERM-04", "Webhook approval allow / deny with reason", "P0", "Security", "B",
   "toolPolicies={calculator: webhook}, approvalWebhook=http://127.0.0.1:18099/approve?allow=true", ["Call calculator.", "Change to ?allow=false&reason=QA+says+no and repeat.", "inspect webhook"],
   "Allowed, then 'permission denied for calculator: QA says no'. Webhook payload has tool, arguments, sessionId, traceId.")
tc("PERM-05", "Webhook approval fails closed", "P0", "Security", "B", "As PERM-04", ["URL with ?status=500; with ?invalid=1; with an unreachable host."],
   "All denied ('approval webhook returned an invalid response' / 'unreachable').")
tc("PERM-06", "Webhook policy requires URL", "P1", "Validation", "L", "", ["Set a tool to webhook with no approval URL."], "`permissions.approvalWebhook: required when a tool uses the webhook policy`.")
tc("PERM-07", "Workspace root", "P0", "Security", "B", "workspaceRoot=./qa-data/ws2", ["write_file a.txt; shell ls"], "File lands in qa-data/ws2 (created if missing); shell runs there.")
tc("PERM-08", "Permission table in UI lists every tool", "P2", "UI", "L", "qa-kitchen-sink imported", ["Open Permissions."], "Built-ins and HTTP tools listed with allow/deny/webhook selects; MCP group note shown.")
tc("PERM-09", "Default preset policies", "P2", "Functional", "L", "Start from defaults", ["Open Permissions."], "shell and write_file default to deny.")

# =====================================================================
section("GRD", "Guardrails")
tc("GRD-01", "Input keyword block", "P0", "Security", "B", "run qa-kitchen-sink (keyword 'swordfish' → block)",
   ["send \"the password is SwordFish\"", "inspect mock"], "Reply 'QA: request blocked.' finish_reason content_filter; NO upstream call; trace guardrail/block stage=input.")
tc("GRD-02", "Input regex redact", "P0", "Security", "B", "inputRules=[{name: cc, type: regex, pattern: '\\\\d{4}-\\\\d{4}', action: redact}]",
   ["send \"[[echo-messages]] card 1234-5678\"", "inspect mock (message text)"], "Upstream user text contains '[REDACTED]' instead of the number.")
tc("GRD-03", "Input warn", "P2", "Functional", "B", "rule action warn", ["Send matching text."], "Request proceeds unchanged; trace guardrail/warn.")
tc("GRD-04", "Output redact", "P0", "Security", "B", "qa-kitchen-sink (ID-[0-9]{4} → redact)", ["send \"[[say:your id is ID-1234 ok]]\""], "'your id is [REDACTED] ok'.")
tc("GRD-05", "Output block", "P0", "Security", "B", "outputRules=[{name: no-secret, type: keyword, pattern: topsecret, action: block}]", ["send \"[[say:this is TOPSECRET]]\""],
   "'QA: request blocked.' with finish_reason content_filter.")
tc("GRD-06", "PII redaction", "P0", "Security", "B", "guardrails.redactPII=true",
   ["send \"[[say:mail a@b.com call +1 415 555 0100 card 4111 1111 1111 1111 ssn 123-45-6789]]\"", "Also send that text as input with [[echo-messages]]."],
   "Email, phone, card (Luhn-valid) and SSN all become [REDACTED] in output and in the upstream input, with surrounding spaces intact ('card [REDACTED] ssn [REDACTED]').")
tc("GRD-13", "PII redaction leaves look-alikes alone", "P1", "Regression", "B", "guardrails.redactPII=true",
   ["send \"[[say:pi is 3.14159265358979, order 1234567890123, year 2026, card 4111 1111 1111 1112]]\""],
   "Unchanged: decimals, plain long numbers, years, and card-like numbers that fail the Luhn check are not redacted.")
tc("GRD-07", "Max input chars", "P1", "Functional", "B", "maxInputChars=10", ["send \"this is longer than ten\""], "Blocked (content_filter) without calling the model.")
tc("GRD-08", "Only the latest user message is checked", "P2", "Functional", "B", "keyword block 'swordfish'", ["History with 'swordfish' in an earlier user turn, clean latest message."], "Not blocked.")
tc("GRD-09", "Output rules force buffered streaming", "P1", "Functional", "B", "any output rule", ["send \"[[say:hello world]]\" --stream --raw"], "Content arrives once, already filtered.")
tc("GRD-10", "Streaming block message", "P1", "Functional", "B", "GRD-01 spec", ["send \"swordfish\" --stream"], "Stream contains the blocked message and finish_reason content_filter, then [DONE].")
tc("GRD-11", "Rule validation", "P1", "Validation", "L", "", ["Regex `(`; type 'glob'; action 'drop'."], "Errors: bad regex; type must be regex|keyword; action must be block|redact|warn.")
tc("GRD-12", "Custom blocked message", "P2", "Functional", "B", "blockedMessage='Nope.'", ["Trigger a block."], "'Nope.'")

# =====================================================================
section("HOOK", "Hooks", "Webhook fixture on :18099. `inspect webhook` shows what each hook sent.")
tc("HOOK-01", "Every event fires with a log hook", "P0", "Functional", "B", "One log hook per event: turn_start, pre_step, pre_tool, post_tool, turn_end, on_error.",
   ["Run a tool-using request, then a failing one ([[status:500]], retries 0)."], "Agent log has 'hook' lines for all six events with the trace id.")
tc("HOOK-02", "Webhook payload shape", "P0", "Functional", "B", "turn_end webhook → http://127.0.0.1:18099/hook", ["send \"hi\" --session h1", "inspect webhook"],
   "Payload {event: turn_end, traceId, sessionId: h1, data: {text, finishReason}}.")
tc("HOOK-03", "turn_start block", "P0", "Functional", "B", "turn_start webhook, blocking, …/hook?action=block&message=closed", ["send \"hi\""], "Reply 'closed', finish_reason content_filter, no model call.")
tc("HOOK-04", "pre_step block", "P1", "Functional", "B", "pre_step blocking webhook ?action=block (no message)", ["send \"hi\""], "Reply 'Stopped by a hook.'")
tc("HOOK-05", "pre_tool block", "P0", "Functional", "B", "pre_tool blocking webhook ?action=block&message=nope", ["send '[[tool:calculator {\"expression\":\"1+1\"}]]'"], "'TOOL_RESULT: Error: blocked by hook: nope'.")
tc("HOOK-06", "turn_start inject is independent of the client policy", "P1", "Functional", "B", "turn_start blocking webhook …/hook?inject=INJECTED",
   ["send \"[[echo-system]]\" --system CLIENT with clientSystemPolicy append, replace and ignore."],
   "Every time: '## Context from hooks\\nINJECTED' present and the base prompt kept (except replace, where CLIENT replaces only the base text).")
tc("HOOK-07", "pre_step inject", "P1", "Functional", "B", "pre_step inject hook type=inject content='STEP-NOTE'", ["send \"[[echo-messages]]\""], "Upstream has an extra system message before the model call.")
tc("HOOK-08", "Non-blocking webhooks don't delay", "P1", "Performance", "B", "turn_end webhook ?delay=3000 blocking=false", ["Time a request."], "Returns immediately; the webhook still receives the call ~3 s later.")
tc("HOOK-09", "Blocking webhook timeout", "P1", "Negative", "B", "pre_tool blocking ?delay=5000 timeoutMs=500", ["Call a tool."], "Warning 'hook failed'; tool still runs (fail open for hooks).")
tc("HOOK-10", "Failing webhook ignored", "P2", "Negative", "B", "?status=500 and unreachable URL", ["Requests still succeed."], "Warnings logged, requests unaffected.")
tc("HOOK-11", "post_tool payload", "P2", "Functional", "B", "post_tool webhook", ["Successful and failing tool calls."], "data.result (≤2000 chars) on success, data.error on failure.")
tc("HOOK-12", "on_error payload", "P1", "Functional", "B", "on_error webhook; model retries 0", ["send \"[[status:500]]\""], "Webhook receives on_error with data.error.")
tc("HOOK-13", "Validation", "P1", "Validation", "L", "", ["Event 'before_reply'; type 'email'; webhook without URL."], "Errors listing allowed events/types; url required for webhook.")

# =====================================================================
section("OBS", "Observability")
tc("OBS-01", "Trace file events", "P0", "Functional", "B", "observability.traceFile=./qa-data/trace.jsonl",
   ["Run plan_execute + tool + self-check request.", "inspect trace -n 20"], "JSONL with turn/start, plan, llm/response (model, ms, usage), tool/result, selfcheck/revise or none, turn/end — all sharing one traceId.")
tc("OBS-02", "Trace id header", "P1", "Functional", "B", "traceHeader=true then false", ["send \"hi\" (non-stream and --stream)"], "X-Harness-Trace-Id present (also on streams, plus harness_trace_id in the final chunk) / absent when off.")
tc("OBS-03", "Usage reporting", "P0", "Functional", "B", "includeUsage=true", ["Non-stream; stream with stream_options.include_usage; stream without it."],
   "usage object present; usage chunk sent; no usage chunk.")
tc("OBS-04", "Usage off", "P2", "Functional", "B", "includeUsage=false", ["Non-stream request."], "No usage field.")
tc("OBS-05", "Log levels", "P1", "Functional", "B", "", ["logLevel=debug, then error."], "debug shows per-event lines and MCP stderr; error shows only errors.")
tc("OBS-06", "JSON log format", "P2", "Functional", "B", "logFormat=json", ["Start and send a request."], "Each stderr line is valid JSON.")
tc("OBS-07", "Unwritable trace path", "P2", "Negative", "B", "traceFile=/root/nope/trace.jsonl", ["Start."], "Warning 'cannot open trace file'; agent still runs.")
tc("OBS-08", "Validation", "P2", "Validation", "L", "", ["logLevel 'verbose'; logFormat 'xml'."], "Errors listing allowed values.")

# =====================================================================
section("PLAY", "Test playground (builder)", "Env L, fixtures running, a spec pointing at the mock (import qa-base).")
tc("PLAY-01", "Start a test instance", "P0", "Functional", "L", "", ["Open Test → Start test instance."], "Status 'Test instance running at /preview/<id>/v1'; chat box focused.")
tc("PLAY-02", "Chat with streaming and activity", "P0", "Functional", "L", "", ["Send '[[tool:calculator {\"expression\":\"2^10\"}]]'."],
   "Agent activity panel shows the tool call (activity is always streamed in previews); answer 'TOOL_RESULT: 1024'; trace id shown.")
tc("PLAY-03", "Markdown rendering", "P2", "UI", "L", "", ["Send '[[say:**bold** and `code`]]' and a fenced code block."], "Bold, inline code and code blocks render; HTML is escaped.")
tc("PLAY-04", "Stale spec warning", "P1", "Functional", "L", "Instance running.", ["Change any field."], "'Spec changed since start — restart to apply.'; Restart applies the change.")
tc("PLAY-05", "Reset conversation", "P1", "Functional", "L", "", ["Chat, click Reset conversation."], "Messages cleared; a new session id is used.")
tc("PLAY-06", "Server sessions in playground", "P1", "Functional", "L", "sessions.mode=server", ["Two turns, second '[[echo-messages]]'."], "History is kept (X-Session-Id sent per conversation).")
tc("PLAY-07", "Invalid spec cannot start", "P1", "Negative", "L", "Spec with errors.", ["Start test instance."], "'The spec has problems:' with the error list; no instance.")
tc("PLAY-08", "Model error shown in chat", "P1", "Negative", "L", "", ["Send '[[status:500]]' with retries 0."], "Assistant bubble shows the error text (no empty grey bubble).")
tc("PLAY-09", "Preview eviction (local: 4)", "P2", "Functional", "L", "", ["Start 5 instances in turn (restart repeatedly)."], "The oldest is evicted; using its URL → 'Test instance not found or expired. Start a new one.'")
tc("PLAY-10", "Builder env vars used locally", "P1", "Functional", "L", "Builder started with QA_MODEL_KEY=sk-local.", ["Preview with [[echo-params]]."], "Mock sees Bearer sk-local… (local mode only).")

# =====================================================================
section("EXP", "Build and export")
tc("EXP-01", "OS cards and processor options", "P0", "Functional", "L", "", ["Click macOS, Ubuntu / Linux, Windows."],
   "macOS: Apple Silicon (M1 and later), Intel. Linux: x86-64, ARM64. Windows: x64, ARM64. Host target marked '(this machine)' locally.")
tc("EXP-02", "Build matrix: all 6 targets", "P0", "Functional", "L", "Any valid preset.",
   ["Build each OS/processor combination.", "Run `file <download>` on each."],
   "Files <name>-darwin-arm64, -darwin-amd64, -linux-amd64, -linux-arm64, -windows-amd64.exe, -windows-arm64.exe; `file` reports Mach-O arm64/x86_64, ELF x86-64/aarch64, PE32+ x86-64/Aarch64. ~7–8 MB each.")
tc("EXP-03", "Build progress and timing", "P1", "UI", "L", "", ["Build; watch the button and progress bar."], "Button 'Building…', elapsed seconds counter, progress bar; result line 'Downloaded <file> · <size> · <target>'.")
tc("EXP-04", "Build blocked when spec invalid", "P0", "Negative", "L", "Spec with errors.", ["Open Build."], "Pre-flight lists problems; Build button disabled.")
tc("EXP-05", "API rejects invalid spec and target", "P1", "Negative", "L", "",
   ["POST /api/build with name 'bad name!'.", "POST /api/build with os=plan9."], "400 'spec is invalid: …'; 400 'unsupported target plan9/…'.")
tc("EXP-06", "Docker toggle changes the export", "P0", "Functional", "L", "", ["Turn on Include a Dockerfile; build for Windows x64."],
   "Button 'Build Windows (x64) + Docker'; downloads <name>-windows-amd64-docker.zip (application/zip).")
tc("EXP-07", "Docker bundle contents", "P0", "Functional", "L", "Zip from EXP-06.", ["unzip and list."],
   "<name>/: native binary, Dockerfile, docker-compose.yml, .dockerignore, README.md, docker/linux-amd64/agent, docker/linux-arm64/agent; Linux binaries executable after unzip on macOS/Linux.")
tc("EXP-08", "Distroless Dockerfile for tool-free agents", "P1", "Functional", "L", "Minimal preset + Docker.", ["Read the Dockerfile."],
   "busybox stage + gcr.io/distroless/static-debian12:nonroot, COPY --chmod docker/linux-${TARGETARCH}/agent, WORKDIR/VOLUME /data, ENV PORT, EXPOSE, HEALTHCHECK --healthcheck, ENTRYPOINT --host 0.0.0.0.")
tc("EXP-09", "Debian Dockerfile when the agent runs programs", "P1", "Functional", "L", "Coding preset + Docker; add 'terraform' to the shell allow-list.", ["Read the Dockerfile."],
   "debian:bookworm-slim installing ca-certificates tzdata git golang-go npm python3 python3-pytest; '# TODO: install \"terraform\"'; non-root user.")
tc("EXP-10", "Docker image builds and runs (amd64 host)", "P0", "Functional", "D", "Docker 23+ on Ubuntu x86-64; any bundle.",
   ["`docker compose up --build` in the unzipped folder with the key env var exported.", "`curl localhost:<port>/v1/models`", "`docker ps` health column."],
   "Image builds using docker/linux-amd64; container healthy; API answers; data volume created.")
tc("EXP-11", "Docker image on Apple Silicon (arm64)", "P0", "Compatibility", "D", "Docker Desktop on an M-series Mac.", ["As EXP-10."], "Builds using docker/linux-arm64 (no emulation warning); works.")
tc("EXP-12", "Docker on Windows", "P1", "Compatibility", "D", "Docker Desktop on Windows; PowerShell.", ["Expand-Archive, set $env:KEY, docker compose up --build."], "Works; --chmod sets the executable bit even though Windows lost it.")
tc("EXP-13", "Secrets not baked into the image", "P0", "Security", "D", "Image from EXP-10.", ["`docker history --no-trunc <image>` and `docker inspect`."], "No API key values present; only env var names.")
tc("EXP-14", "Data persists across container restarts", "P1", "Functional", "D", "Support preset (memory + server sessions) in Docker.", ["Save a memory, `docker compose down && up`, ask again."], "Memory and sessions still there (named volume).")
tc("EXP-15", "Workspace mount hint", "P2", "Functional", "L", "Coding preset bundle.", ["Read docker-compose.yml."], "Commented '# - ./my-project:/data/workspace' line present; absent for tool-free agents.")
tc("EXP-16", "How-to-run matches the download", "P1", "Functional", "L", "", ["For each OS, read the 'How to run' panel."],
   "Uses the real filename (<name>-<os>-<arch>[.exe]); macOS shows xattr tip; Windows shows Unblock-File and PowerShell env syntax; env vars listed; Docker section only when the toggle is on.")
tc("EXP-17", "Copy buttons on code blocks", "P2", "UI", "L", "", ["Click Copy on each block."], "Clipboard holds the exact command.")
tc("EXP-18", "Target and Docker choice remembered", "P2", "Functional", "L", "", ["Pick Linux ARM64 + Docker, reload."], "Same selection restored.")
tc("EXP-19", "Default target in public mode follows the visitor", "P1", "Functional", "P", "Public builder on Linux; visit from a Mac/Windows browser.", ["Open Build."], "Default OS matches the visitor's OS (not the server's); no '(this machine)' labels.")
tc("EXP-20", "Uppercase names in Docker", "P2", "Functional", "L", "Agent name 'My-Agent' + Docker.", ["Read Dockerfile/compose."], "Image and service names lowercased (my-agent).")

# =====================================================================
section("BIN", "Agent binary: CLI and platforms")
tc("BIN-01", "Runs on macOS Apple Silicon", "P0", "Compatibility", "B", "Downloaded darwin-arm64 binary.", ["chmod +x; xattr -d com.apple.quarantine (if blocked); run; curl /v1/models."], "Starts, logs 'harness listening', API works.")
tc("BIN-02", "Runs on macOS Intel", "P1", "Compatibility", "B", "darwin-amd64 on an Intel Mac (or Rosetta).", ["As BIN-01."], "Works.")
tc("BIN-03", "Runs on Ubuntu x86-64", "P0", "Compatibility", "B", "Ubuntu 22.04/24.04.", ["chmod +x; run; curl."], "Works with no extra packages (static binary).")
tc("BIN-04", "Runs on Linux ARM64", "P1", "Compatibility", "B", "Graviton / Raspberry Pi 64-bit.", ["As BIN-03."], "Works.")
tc("BIN-05", "Runs on Windows x64", "P0", "Compatibility", "B", "Windows 10/11 PowerShell.", ["Unblock-File; $env:KEY=…; .\\<name>.exe; curl.exe …"], "Works; firewall prompt may appear for 0.0.0.0.")
tc("BIN-06", "Runs on Windows ARM64", "P2", "Compatibility", "B", "Snapdragon laptop.", ["As BIN-05."], "Works.")
tc("BIN-07", "--version / --check / --print-spec", "P1", "Functional", "B", "", ["Run each flag."], "'openharness runtime 1'; 'spec OK: <name>'; pretty JSON of the embedded spec with key env var names only.")
tc("BIN-08", "--port, --host and PORT env", "P1", "Functional", "B", "", ["--port 9001", "PORT=9002 (no flag)", "--port 9003 with PORT=9002", "--host 0.0.0.0"], "Listens on 9001, 9002, 9003 (flag wins), all interfaces.")
tc("BIN-09", "--spec override", "P1", "Functional", "B", "", ["Run a built binary with --spec qa/fixtures/specs/qa-base.json."], "Serves qa-agent instead of the embedded spec.")
tc("BIN-10", "Invalid --spec", "P2", "Negative", "B", "", ["--spec missing.json; --spec file with invalid JSON; spec with errors."], "Clear error and exit 1; spec errors printed one per line ('spec error: …').")
tc("BIN-11", "--healthcheck", "P1", "Functional", "B", "", ["With the agent running: `agent --healthcheck --port <p>`.", "With nothing running."], "Exit 0 / exit 1 with 'unhealthy: …'.")
tc("BIN-12", "Graceful shutdown", "P1", "Resilience", "B", "Agent mid-stream on a slow request.", ["Ctrl-C / SIGTERM."], "Exits within ~10 s; MCP children stopped; no corrupted session/memory files.")
tc("BIN-13", "Port already in use", "P2", "Negative", "B", "", ["Start two agents on the same port."], "Second exits with 'address already in use'.")
tc("BIN-14", "Binary without a spec", "P2", "Negative", "B", "`go run ./cmd/harness` (no embedded spec).", ["Run with no flags."], "'no spec embedded in this binary; pass --spec harness.json'.")

# =====================================================================
section("API", "OpenAI API compatibility")
tc("API-01", "OpenAI Python SDK (non-stream + stream)", "P0", "Compatibility", "B", "pip install openai; run qa-base",
   ["client = OpenAI(base_url='http://127.0.0.1:8090/v1', api_key='x'); chat.completions.create(…)", "Same with stream=True."], "Both work; streamed deltas concatenate to the answer.")
tc("API-02", "OpenAI Node SDK", "P1", "Compatibility", "B", "npm i openai", ["Non-stream and stream calls."], "Both work.")
tc("API-03", "LangChain ChatOpenAI with tools", "P1", "Compatibility", "B", "langchain-openai", ["bind_tools([get_weather]) and invoke a prompt that triggers it ([[tool:get_weather …]])."], "AIMessage.tool_calls populated (client tool passthrough).")
tc("API-04", "SSE format", "P0", "Compatibility", "B", "", ["send … --stream --raw"],
   "First chunk delta {role: assistant, content: ''}; chunks have id/object chat.completion.chunk/created/model; final chunk finish_reason; optional usage chunk with empty choices; ends 'data: [DONE]'.")
tc("API-05", "Streamed client tool_calls format", "P1", "Compatibility", "B", "TLP-05 with --stream --raw", ["Inspect chunks."], "tool_calls delta with index, id, type function, name, arguments; finish_reason tool_calls.")
tc("API-06", "Multimodal content arrays", "P1", "Compatibility", "B", "",
   ["Send content: [{type: text, text: '[[echo-messages]]'}, {type: image_url, image_url: {url: 'data:image/png;base64,…'}}]."], "Accepted; parts forwarded upstream for OpenAI-compatible providers.")
tc("API-07", "Request validation", "P1", "Negative", "B", "", ["Body `{bad`", "messages: []", "Body > 32 MB"], "400 with OpenAI-style error {error: {message, type: invalid_request_error}}.")
tc("API-08", "Upstream error mapping", "P1", "Negative", "B", "retries 0", ["send \"[[status:500]]\" non-stream and --stream."], "Non-stream: 502 server_error. Stream: an error chunk then [DONE].")
tc("API-09", "Keep-alive on long turns", "P2", "Resilience", "B", "", ["send \"[[slow:20]] hi\" --stream --raw"], "': keep-alive' comment lines every ~15 s; connection stays open.")
tc("API-10", "Client disconnect cancels work", "P2", "Resilience", "B", "", ["Start a [[slow:20]] stream and kill the client after 2 s."], "Agent logs the cancelled turn; no goroutine/process leak (memory stable).")
tc("API-11", "Concurrent requests", "P1", "Performance", "B", "", ["20 parallel requests with tools."], "All succeed with correct, non-mixed answers; trace ids distinct.")
tc("API-12", "Unknown endpoints", "P2", "Negative", "B", "", ["GET /v1/chat/completions; POST /v1/embeddings."], "405 / 404, no crash.")

# =====================================================================
section("PUB", "Public mode security", "Builder with `--public` (or the deployed image). Fixtures on localhost are intentionally unreachable from previews.")
tc("PUB-01", "Catalog reports public mode", "P0", "Functional", "P", "", ["GET /api/catalog."], "\"public\": true.")
tc("PUB-02", "Test step asks for the visitor's key", "P0", "Security", "P", "", ["Open Test."],
   "Shared-server notice and a password field 'Your model API key for this test'; key is not saved to localStorage, the spec, or exports.")
tc("PUB-03", "Server env vars are never read", "P0", "Security", "P", "Builder started with SECRET_X=leakme.",
   ["Preview a spec with model.apiKeyEnv=SECRET_X and an HTTP tool header 'X: ${SECRET_X}' pointing at a public request bin.", "Chat to trigger both."],
   "Neither the model call nor the tool call carries 'leakme' (empty values).")
tc("PUB-04", "Visitor key is used", "P0", "Functional", "P", "Real provider key (LIVE env).", ["Enter key, start preview, chat."], "Model answers; the key is used for the primary and fallbacks without inline keys.")
tc("PUB-05", "Shell and stdio MCP removed with notes", "P0", "Security", "P", "Coding preset + a stdio MCP server.", ["Start preview."],
   "Notes: 'Removed the shell tool…' and 'Skipped stdio MCP server …'. Tool list excludes them.")
tc("PUB-06", "SSRF: private networks blocked", "P0", "Security", "P", "Spec with http_fetch allowed and the mock as base URL.",
   ["Use model base URL http://127.0.0.1:18098/v1.", "http_fetch http://169.254.169.254/latest/meta-data/, http://10.0.0.1/, http://[::1]/, http://localhost/.", "A public hostname whose DNS resolves to 127.0.0.1 (e.g. localtest.me)."],
   "All refused with 'destination address is not allowed' — including the DNS-rebinding style hostname. Public hosts work.")
tc("PUB-07", "Hooks / approvals / HTTP MCP can't reach private IPs", "P0", "Security", "P", "", ["Point a hook, an approval webhook and an http MCP server at 127.0.0.1."], "All blocked (approval → denied, fail closed).")
tc("PUB-08", "Paths sandboxed per preview", "P0", "Security", "P", "Spec with memory.path=/etc/passwd, sessions.dir=/, traceFile=/tmp/x, workspaceRoot=/.",
   ["Start preview; use memory and file tools."], "Everything lands in the preview's temp dir; no files outside it are created or read.")
tc("PUB-09", "Temp dirs removed", "P1", "Security", "P", "", ["Start 3 previews from one IP; check $TMPDIR/oh-preview-*."], "At most 2 dirs per visitor remain; evicted/expired previews delete theirs.")
tc("PUB-10", "Preview auth forced off", "P2", "Functional", "P", "Spec with authMode=bearer.", ["Chat in preview."], "Works without a key (preview URL is unguessable).")
tc("PUB-11", "Rate limit: test instances", "P0", "Security", "P", "", ["31 POST /api/preview from one IP within an hour."], "30 succeed, the 31st → 429 with Retry-After.")
tc("PUB-12", "Rate limit: builds", "P0", "Security", "P", "", ["21 builds from one IP."], "21st → 429 'Too many builds. Try again in Ns.'")
tc("PUB-13", "Rate limit: messages", "P1", "Security", "P", "", ["Send >30 chat messages quickly to a preview."], "After the burst (30), 429 in OpenAI error shape; refills ~1 per 30 s.")
tc("PUB-14", "Limits are per visitor", "P1", "Security", "P", "Two client IPs.", ["Exhaust limits from IP A; use IP B."], "B unaffected.")
tc("PUB-15", "X-Forwarded-For trust", "P0", "Security", "P", "",
   ["With --trust-proxy off: send 31 previews with rotating X-Forwarded-For.", "Behind the Coolify proxy: confirm limits apply per real client."],
   "Without trust, spoofed headers are ignored (still limited). Behind the proxy, the last hop (added by the proxy) is used.")
tc("PUB-16", "Preview per-IP cap and global cap", "P1", "Functional", "P", "", ["Start 3 previews from one IP."], "The first is evicted (404 'Test instance not found or expired…').")
tc("PUB-17", "Idle previews expire", "P2", "Functional", "P", "", ["Start a preview, wait 31 min idle."], "Closed and its temp dir removed.")
tc("PUB-18", "Security headers", "P1", "Security", "P", "", ["curl -I /, /api/catalog."], "X-Content-Type-Options nosniff, X-Frame-Options DENY, Referrer-Policy no-referrer.")
tc("PUB-19", "Downloaded binaries are not sandboxed", "P1", "Functional", "P", "Coding preset.", ["Build in public mode; run locally."], "shell, stdio MCP and env vars work in the binary (sandbox applies to previews only).")
tc("PUB-20", "Large / malformed payloads", "P1", "Security", "P", "", ["POST /api/build with 9 MB body; with invalid JSON."], "Rejected with 400; server stays healthy.")

# =====================================================================
section("DEP", "Deployment (Coolify) and operations")
tc("DEP-01", "Compose deploy succeeds", "P0", "Functional", "D", "Coolify, Docker Compose build pack, 2 vCPU / 4 GB + 4 GB swap.",
   ["Deploy.", "Watch logs."], "go test -short passes; six 'warmed …' lines; container healthy.")
tc("DEP-02", "HTTPS and domain", "P0", "Functional", "D", "", ["curl https://<domain>/healthz", "curl -I http://<domain>/"], "{\"status\":\"ok\"}; valid certificate; HTTP redirects to HTTPS.")
tc("DEP-03", "Port not published on host", "P0", "Security", "D", "", ["From outside: curl http://<server-ip>:7070/healthz", "`docker ps` port column."], "Connection refused/timeout; only 7070/tcp exposed (no 0.0.0.0 mapping).")
tc("DEP-04", "Resource limits applied", "P1", "Functional", "D", "", ["`docker inspect` → HostConfig.NanoCpus / Memory."], "1.5 CPUs, 2560 MiB.")
tc("DEP-05", "Build concurrency 1", "P1", "Functional", "D", "", ["Start two builds at once from two browsers."], "Second waits for the first; both complete.")
tc("DEP-06", "Warm cache persists in volume", "P1", "Performance", "D", "", ["Redeploy; time the first build after it."], "~1–3 s (cache on /data volume), not ~6 s+.")
tc("DEP-07", "Public mode on by default", "P0", "Security", "D", "", ["GET /api/catalog; open Test."], "public: true; key field shown; shell removed in previews.")
tc("DEP-08", "Client IP behind proxy", "P1", "Security", "D", "", ["Hit a rate limit from your IP; another network still works."], "Limits are per real visitor IP, not per proxy IP.")
tc("DEP-09", "Redeploy on push (if webhook configured)", "P2", "Functional", "D", "", ["Push a trivial commit."], "Coolify redeploys automatically; site stays healthy.")
tc("DEP-10", "Container restart recovers", "P1", "Resilience", "D", "", ["Restart the container from Coolify."], "Healthy again in < 30 s; previews from before are gone (in-memory) and the UI handles it.")
tc("DEP-11", "Memory headroom during builds", "P1", "Performance", "D", "", ["Run 10 builds back to back while watching `free -h` / Coolify metrics."], "No OOM kills; swap use stays modest; Coolify UI responsive.")
tc("DEP-13", "Link previews on social platforms", "P1", "Functional", "D", "",
   ["`curl -s https://<domain>/ | grep -E 'og:|twitter:'`; open /og.png, /favicon.ico, /apple-touch-icon.png, /robots.txt.",
    "Check https://www.opengraph.xyz (all platforms), Facebook Sharing Debugger and LinkedIn Post Inspector (both have a re-scrape button).",
    "Paste the link into WhatsApp, Telegram, Slack, Discord, X (post composer) and iMessage. Add ?v=N to bypass a cached empty preview."],
   "All tags use https://<domain>/…; og.png is 1200×630; icons and robots.txt return 200. Every platform shows the large card with title, description and image (Discord with a black side bar).")
tc("DEP-14", "Google Analytics opt-in", "P2", "Functional", "D", "GA4 property; OPENHARNESS_GA_ID set in Coolify.",
   ["View page source: gtag script present with your ID.", "Choose a preset, visit steps, test, build.", "GA → Realtime / DebugView.",
    "Remove the variable, redeploy, view source."],
   "Events page_view (/step/<id>), select_preset, test_start, test_message, build_download appear with only fixed parameters (no names, prompts or keys). Without the variable, no googletagmanager script loads.")
tc("DEP-12", "Logs readable in Coolify", "P2", "Functional", "D", "", ["Open the resource logs."], "Startup line 'OpenHarness builder running … mode=public'; build/preview lines with agent names.")

# =====================================================================
section("PERF", "Performance and resilience")
tc("PERF-01", "Warm build time", "P1", "Performance", "L", "Cache warm (`go run ./cmd/openharness --warm` once).", ["Build 5 times for the host target."], "Each ≤ 3 s locally (X-Build-Ms header).")
tc("PERF-02", "Cold build time", "P2", "Performance", "L", "Fresh cache dir.", ["First build."], "Completes (≈5–40 s depending on CPU).")
tc("PERF-03", "Docker bundle build time", "P2", "Performance", "L", "Warm cache.", ["Build with Docker on."], "≈3 builds' worth (few seconds warm).")
tc("PERF-04", "Agent latency overhead", "P1", "Performance", "B", "run qa-base", ["100 sequential 'hi' requests; compare with direct mock latency."], "Harness overhead ≤ ~5 ms median.")
tc("PERF-05", "Agent under load", "P1", "Performance", "B", "", ["50 concurrent streams for 1 min (tool-using)."], "No errors; memory stable; p95 latency reasonable.")
tc("PERF-06", "Builder under load", "P2", "Performance", "L", "", ["20 parallel /api/validate and 10 previews."], "All succeed; UI responsive.")
tc("PERF-07", "Large spec", "P2", "Performance", "L", "Spec with 50 skills, 30 HTTP tools, 20 hooks.", ["Edit, validate, preview, build."], "UI stays responsive; build succeeds (spec embedded via ldflags).")
tc("PERF-08", "Long conversation", "P2", "Performance", "B", "Server sessions, sliding window.", ["500-turn scripted conversation."], "Steady latency; session file grows linearly; compaction keeps upstream size bounded.")

# =====================================================================
section("LIVE", "Live provider smoke tests", "Real keys, real models. Run on the deployed site and on a downloaded binary.")
tc("LIVE-01", "DeepSeek", "P0", "Compatibility", "B", "DEEPSEEK_API_KEY; Research preset.", ["Ask a question needing http_fetch and calculator."], "Plan shown, tools called, sensible cited answer, self-check line.")
tc("LIVE-02", "OpenAI", "P0", "Compatibility", "B", "OPENAI_API_KEY; OpenAI quick-fill.", ["Tool-using question; streaming."], "Works; usage reported.")
tc("LIVE-03", "Anthropic", "P0", "Compatibility", "B", "ANTHROPIC_API_KEY; Anthropic quick-fill.", ["Tool-using question; streaming; image input."], "Works with tool use and streaming.")
tc("LIVE-04", "Ollama (local, keyless)", "P1", "Compatibility", "B", "Ollama running llama3.1.", ["Basic chat and a calculator call."], "Works without an API key.")
tc("LIVE-05", "OpenRouter", "P2", "Compatibility", "B", "OPENROUTER key.", ["Basic chat."], "Works.")
tc("LIVE-06", "Fallback to a real provider", "P1", "Compatibility", "B", "Primary with a bad key, fallback with a good one.", ["Chat."], "Fallback answers after the primary's 401.")
tc("LIVE-07", "Real MCP server", "P1", "Compatibility", "B", "e.g. @modelcontextprotocol/server-filesystem via npx (stdio).", ["List and call its tools."], "Tools appear as mcp_<server>_<tool> and work.")
tc("LIVE-08", "Each preset end to end on the public site", "P0", "Functional", "D", "Your key.", ["For each preset: test chat, build for your OS, run the binary, call it with the OpenAI SDK."], "All four work end to end.")


# =====================================================================
def github_slug(heading):
    """GitHub's heading anchor: lowercase, drop punctuation, each space becomes '-'."""
    out = []
    for ch in heading.lower():
        if ch.isalnum() or ch in "-_":
            out.append(ch)
        elif ch == " ":
            out.append("-")
    return "".join(out)


def write():
    here = os.path.dirname(os.path.abspath(__file__))
    by = OrderedDict((k, []) for k in SECTIONS)
    for c in CASES:
        by[c["section"]].append(c)
    ids = [c["id"] for c in CASES]
    assert len(ids) == len(set(ids)), "duplicate ids"

    pri = {p: sum(1 for c in CASES if c["priority"] == p) for p in ("P0", "P1", "P2")}
    md = ["# OpenHarness QA test set", "",
          "> Generated by `python3 qa/generate.py` — edit that file, not this one. How to run: [README.md](README.md).", "",
          "**%d cases** · P0 %d · P1 %d · P2 %d" % (len(CASES), pri["P0"], pri["P1"], pri["P2"]), "",
          "| Section | Cases | P0 |", "|---|---|---|"]
    for code, (title, _) in SECTIONS.items():
        cs = by[code]
        anchor = github_slug("%s · %s" % (code, title))
        md.append("| [%s · %s](#%s) | %d | %d |" % (code, title, anchor, len(cs), sum(1 for c in cs if c["priority"] == "P0")))
    md += ["", "Env: **L** local builder · **P** public builder · **B** agent binary / `qa.py run` · **D** deployed (Docker/Coolify).", ""]
    for code, (title, intro) in SECTIONS.items():
        md += ["## %s · %s" % (code, title), ""]
        if intro:
            md += ["_%s_" % intro, ""]
        for c in by[code]:
            md.append("### %s · %s" % (c["id"], c["title"]))
            md.append("`%s` · %s · Env %s" % (c["priority"], c["type"], c["env"]))
            md.append("")
            if c["setup"]:
                md.append("- **Setup:** " + c["setup"])
            md.append("- **Steps:**")
            for i, s in enumerate(c["steps"], 1):
                md.append("  %d. %s" % (i, s))
            md.append("- **Expected:** " + c["expected"])
            md.append("")
    with open(os.path.join(here, "TEST_CASES.md"), "w") as f:
        f.write("\n".join(md))

    with open(os.path.join(here, "test-cases.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["ID", "Section", "Title", "Priority", "Type", "Env", "Setup", "Steps", "Expected",
                    "Status", "Tester", "Date", "Build/commit", "Notes"])
        for c in CASES:
            w.writerow([c["id"], SECTIONS[c["section"]][0], c["title"], c["priority"], c["type"], c["env"], c["setup"],
                        "\n".join("%d. %s" % (i, s) for i, s in enumerate(c["steps"], 1)), c["expected"], "", "", "", "", ""])
    print("wrote %d cases (P0 %d, P1 %d, P2 %d) to qa/TEST_CASES.md and qa/test-cases.csv" % (len(CASES), pri["P0"], pri["P1"], pri["P2"]))


if __name__ == "__main__":
    write()
