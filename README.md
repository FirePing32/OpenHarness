# OpenHarness

Build an AI agent by customizing **every component of its harness**: model, prompt, tools, skills, memory, sessions, context management, loop, sub-agents, permissions, guardrails, hooks and observability. Test it live, then download it as a **single binary that serves an OpenAI-compatible API**.

- Research behind the design: [docs/RESEARCH.md](docs/RESEARCH.md)
- Public deployment (Coolify / Docker): [docs/DEPLOY.md](docs/DEPLOY.md)

## Run the builder locally

```bash
go run ./cmd/openharness
```

Then open http://127.0.0.1:7070. The journey:

1. Pick a preset.
2. Walk through the 14 harness components.
3. Chat with a live test instance.
4. Build for your OS and download.

Building binaries requires Go 1.26+ wherever the builder runs. In local mode, test instances use the builder's own environment variables (e.g. `DEEPSEEK_API_KEY`).

Flags:
- `--addr`
- `--public` (sandboxed previews and rate limits; see DEPLOY.md)
- `--trust-proxy`
- `--max-previews`
- `--warm` (pre-fill the build cache, then exit)

## Use a downloaded agent

```bash
chmod +x ./my-agent-darwin-arm64
export DEEPSEEK_API_KEY=...       # whichever env var your spec names
./my-agent-darwin-arm64 --port 8080
```

```bash
curl localhost:8080/v1/chat/completions -H 'Content-Type: application/json' \
  -d '{"model":"my-agent","messages":[{"role":"user","content":"hi"}],"stream":true}'
```

```python
from openai import OpenAI
client = OpenAI(base_url="http://localhost:8080/v1", api_key="unused-unless-auth-enabled")
print(client.chat.completions.create(model="my-agent", messages=[{"role": "user", "content": "hi"}]).choices[0].message.content)
```

Endpoints:
- `POST /v1/chat/completions` (streaming, `tools` passthrough, `stream_options.include_usage`)
- `GET /v1/models`
- `GET /v1/harness` (the running spec, secrets stripped)
- `GET /healthz`

Binary flags:
- `--spec file.json` (override the embedded spec)
- `--host`, `--port` (or `PORT`)
- `--print-spec`, `--check`, `--version`

On macOS, a downloaded binary may need `xattr -d com.apple.quarantine ./my-agent-*` before it will run.

## Layout

| Path | What |
|---|---|
| `spec/` | HarnessSpec schema, validation, component catalog, presets |
| `runtime/` | Engine: agent loop, model adapters (OpenAI-compatible, Anthropic), tools, MCP client, memory, sessions, compaction, guardrails, hooks, permissions, network sandbox, OpenAI server |
| `cmd/harness/` | The agent binary (spec embedded at build time via `-ldflags`) |
| `builder/` | Cross-compiles a spec into a binary from the embedded runtime source |
| `cmd/openharness/` | The builder web server (local and public mode) |
| `web/` | Builder UI: plain HTML/CSS/JS, embedded into the server binary |

```bash
go test ./...
```
