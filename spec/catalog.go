package spec

// Component is one customizable harness component as shown in the builder
// journey. Seam names the equivalent capability seam in DeepSeek Harness.
type Component struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Seam    string `json:"seam"`
	Why     string `json:"why"`
}

// Components is ordered as the user journey walks through them.
var Components = []Component{
	{"identity", "Identity & API", "Name the agent and shape the OpenAI-compatible endpoint it serves.",
		"ctx.webServer · dsh profiles (web / headless / sdk / acp)",
		"The harness is what clients talk to. Here the surface is /v1/chat/completions, so any OpenAI SDK, LangChain, or IDE plugin can use your agent unchanged."},
	{"model", "Model adapter", "Pick the provider, model, sampling and fallback chain.",
		"ctx.llm · llm-deepseek · ctx.subagentModelSelection",
		"The model is a swappable plugin. Fallbacks give you routing when a provider is down or rate-limited."},
	{"prompt", "System prompt", "Compose the instructions the model sees on every request.",
		"ctx.systemPrompt · agent-instructions",
		"Prompt assembly is ordered sections: base instructions, date, tool summary, skills index, memory, then the client's own system message."},
	{"tools", "Tools", "Enable built-in tools, add HTTP tools and MCP servers.",
		"ctx.tools · tool-fs · tool-bash · tool-web · ctx.mcpManager",
		"Tools are how the agent acts. Results pass through a pipeline that truncates and prunes them so they don't swamp the context."},
	{"skills", "Skills", "Package reusable instructions the agent loads on demand.",
		"ctx.skills · skill-filesystem · tool-skill",
		"Progressive disclosure keeps only names and descriptions in context; full instructions load when the model asks for them."},
	{"memory", "Memory", "Static notes plus a persistent memory store the agent can write to.",
		"agent-instructions (AGENTS.md) · ctx.storage",
		"Memory survives across conversations: either fixed notes you write, or facts the agent saves and recalls itself."},
	{"sessions", "Sessions", "Stateless like OpenAI, or server-side conversation history.",
		"ctx.sessions · session-persistence-jsonl",
		"In server mode the binary keeps an append-only log per session, so clients can send only the newest message."},
	{"context", "Context management", "Compaction and tool-result pruning when the window fills up.",
		"ctx.compaction · compaction-basic · compaction-tool-result-pruner · ctx.tokenMeter",
		"Long-running agents rot their own context. Choose between dropping old turns or summarizing them, and shrink stale tool output."},
	{"loop", "Agent loop", "How the agent reasons and acts: ReAct, plan-then-execute, or one shot, plus self-checking.",
		"ctx.agentLoop · core/agent-loop · tool-ralph · plan-mode",
		"The loop decides how many steps, what happens on errors, and whether the agent critiques its own answer before replying."},
	{"subagents", "Sub-agents", "Specialist agents the main agent can delegate to with their own prompt and tools.",
		"ctx.subagents · subagent-spawn-in-process · tool-subagent",
		"Delegation keeps the parent's context clean: each sub-agent runs in its own context and returns only its result."},
	{"permissions", "Permissions & sandbox", "Which tools may run, inside which workspace, with optional webhook approval.",
		"ctx.approval · ctx.sandboxPolicy · ctx.permissionPresets",
		"An API has no human to click Approve, so approvals are allow/deny rules or a webhook that decides. Anything unmatched falls back to the default."},
	{"guardrails", "Guardrails", "Filter or redact what goes in and what comes out.",
		"tools/pre-execute waterfall · agent/pre-step listeners",
		"Rules run before the model sees input and before the client sees output: block, redact, or just log."},
	{"hooks", "Hooks", "Run webhooks, logs or context injections at lifecycle events.",
		"agent/pre-step · tools/pre-execute · tools/post-execute · agent/turn-stopping",
		"Hooks are the general extension point: they can observe, inject context, or veto a step or tool call."},
	{"observability", "Observability", "Logging, JSONL traces and usage reporting.",
		"ctx.sessionTelemetry · session-telemetry-otel",
		"Every step, tool call and model request is written as an event, so you can replay and debug what the agent did."},
}

// Preset is a starting template for the journey.
type Preset struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Spec        *Spec  `json:"spec"`
}

func Presets() []Preset {
	minimal := Default()
	minimal.Name = "minimal-agent"
	minimal.Server.ModelID = "minimal-agent"
	minimal.Description = "Bare loop: one model, no tools. A thin proxy with a better prompt."
	minimal.Tools.Builtins = []string{}
	minimal.Loop.Strategy = "single_shot"
	minimal.Context.Strategy = "none"

	research := Default()
	research.Name = "research-agent"
	research.Server.ModelID = "research-agent"
	research.Description = "Plans, fetches sources from the web, and checks its own answer."
	research.Tools.Builtins = []string{"current_time", "calculator", "http_fetch", "todo"}
	research.Loop.Strategy = "plan_execute"
	research.Loop.SelfCheck.Enabled = true
	research.Context.Strategy = "summarize"
	research.Prompt.System = "You are a careful research assistant. Gather evidence with http_fetch before answering, cite the URLs you used, and separate facts from inference."
	research.Subagents = []SubagentSpec{{
		Name: "summarizer", Description: "Condenses a long source into key facts with citations.",
		SystemPrompt: "Summarize the material you are given into concise bullet points. Keep numbers, names and URLs exact.",
		Tools:        []string{}, MaxSteps: 3,
	}}

	coding := Default()
	coding.Name = "coding-agent"
	coding.Server.ModelID = "coding-agent"
	coding.Description = "Reads and edits files in a workspace and runs allow-listed commands."
	coding.Tools.Builtins = []string{"read_file", "list_dir", "write_file", "shell", "todo"}
	coding.Tools.Files.ReadOnly = false
	coding.Tools.Shell.AllowedCommands = []string{"ls", "cat", "grep", "find", "go", "git", "npm", "python3", "pytest"}
	coding.Permissions.ToolPolicies = map[string]string{"shell": "allow", "write_file": "allow"}
	coding.Loop.MaxSteps = 30
	coding.Loop.MaxToolCalls = 120
	coding.Prompt.System = "You are a coding agent working inside a project workspace. Inspect before you edit, keep changes minimal, and run tests to verify your work."
	coding.Sessions.Mode = "server"
	coding.Memory.Persistent = true

	support := Default()
	support.Name = "support-agent"
	support.Server.ModelID = "support-agent"
	support.Description = "Customer support with skills, memory per user, and PII redaction."
	support.Memory.Persistent = true
	support.Sessions.Mode = "server"
	support.Guardrails.RedactPII = true
	support.Prompt.System = "You are a friendly support agent. Be concise, never invent policy, and escalate when unsure."
	support.Skills.Items = []Skill{{
		Name: "refund_policy", Description: "How to handle refund and return questions.",
		Instructions: "1. Ask for the order number.\n2. Refunds are allowed within 30 days of delivery.\n3. Never promise a refund outside policy; offer escalation instead.",
	}}

	return []Preset{
		{"minimal", "Minimal", minimal.Description, minimal},
		{"research", "Research assistant", research.Description, research},
		{"coding", "Coding agent", coding.Description, coding},
		{"support", "Support agent", support.Description, support},
	}
}
