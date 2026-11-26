// Package spec defines the HarnessSpec: the single declarative document that
// describes every customizable component of an OpenHarness agent. The builder
// UI edits it, the builder compiles it into a binary, and the runtime executes it.
package spec

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const SchemaVersion = "1"

// Spec is the root of a harness definition. Each field maps to one harness
// component (see docs/RESEARCH.md for the mapping to DeepSeek Harness seams).
type Spec struct {
	Version       string            `json:"version"`
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	Server        ServerSpec        `json:"server"`
	Model         ModelSpec         `json:"model"`
	Prompt        PromptSpec        `json:"prompt"`
	Tools         ToolsSpec         `json:"tools"`
	Skills        SkillsSpec        `json:"skills"`
	Memory        MemorySpec        `json:"memory"`
	Sessions      SessionSpec       `json:"sessions"`
	Context       ContextSpec       `json:"context"`
	Loop          LoopSpec          `json:"loop"`
	Subagents     []SubagentSpec    `json:"subagents"`
	Permissions   PermissionSpec    `json:"permissions"`
	Guardrails    GuardrailSpec     `json:"guardrails"`
	Hooks         []HookSpec        `json:"hooks"`
	Observability ObservabilitySpec `json:"observability"`
}

// ServerSpec — the API surface ("UI" seam in DeepSeek Harness; here an
// OpenAI-compatible HTTP server).
type ServerSpec struct {
	Host             string   `json:"host"`
	Port             int      `json:"port"`
	ModelID          string   `json:"modelId"`          // name advertised on /v1/models
	AuthMode         string   `json:"authMode"`         // none | bearer
	APIKeyEnv        string   `json:"apiKeyEnv"`        // env var holding comma-separated accepted keys
	CORSOrigins      []string `json:"corsOrigins"`      // empty = CORS disabled, ["*"] = any
	StreamToolEvents bool     `json:"streamToolEvents"` // surface tool activity as reasoning_content deltas
	RequestTimeout   int      `json:"requestTimeoutSec"`
}

// ModelRef identifies one model endpoint.
type ModelRef struct {
	Provider  string `json:"provider"` // openai | anthropic
	BaseURL   string `json:"baseUrl"`
	Model     string `json:"model"`
	APIKeyEnv string `json:"apiKeyEnv"`
	APIKey    string `json:"apiKey,omitempty"` // inline key (discouraged; baked into the binary)
}

// ModelSpec — the model adapter (ctx.llm) plus routing/fallback and sampling.
type ModelSpec struct {
	ModelRef
	Temperature       *float64          `json:"temperature,omitempty"`
	TopP              *float64          `json:"topP,omitempty"`
	MaxTokens         int               `json:"maxTokens"`
	ReasoningEffort   string            `json:"reasoningEffort,omitempty"` // "", low, medium, high
	ExtraBody         map[string]any    `json:"extraBody,omitempty"`
	ExtraHeaders      map[string]string `json:"extraHeaders,omitempty"`
	TimeoutSec        int               `json:"timeoutSec"`
	Retries           int               `json:"retries"`
	Fallbacks         []ModelRef        `json:"fallbacks"`
	ClientParamPolicy string            `json:"clientParamPolicy"` // respect | ignore — honor client temperature/max_tokens
}

// PromptSpec — system prompt assembly (ctx.systemPrompt).
type PromptSpec struct {
	System             string          `json:"system"`
	Sections           []PromptSection `json:"sections"`
	IncludeDateTime    bool            `json:"includeDateTime"`
	IncludeToolSummary bool            `json:"includeToolSummary"`
	ClientSystemPolicy string          `json:"clientSystemPolicy"` // append | replace | ignore
}

type PromptSection struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Enabled bool   `json:"enabled"`
}

// ToolsSpec — the tool registry (ctx.tools) and its execution pipeline.
type ToolsSpec struct {
	Builtins          []string    `json:"builtins"` // enabled built-in tool names
	HTTPTools         []HTTPTool  `json:"httpTools"`
	MCPServers        []MCPServer `json:"mcpServers"`
	Parallel          bool        `json:"parallel"`
	MaxParallel       int         `json:"maxParallel"`
	ResultMaxChars    int         `json:"resultMaxChars"`
	ResultTruncation  string      `json:"resultTruncation"` // head_tail | head
	TimeoutSec        int         `json:"timeoutSec"`
	ClientToolsPolicy string      `json:"clientToolsPolicy"` // passthrough | ignore
	Shell             ShellConfig `json:"shell"`
	Fetch             FetchConfig `json:"fetch"`
	Files             FilesConfig `json:"files"`
}

type ShellConfig struct {
	AllowedCommands []string `json:"allowedCommands"` // empty = any (only if permissions allow)
	TimeoutSec      int      `json:"timeoutSec"`
}

type FetchConfig struct {
	AllowedDomains []string `json:"allowedDomains"` // empty = any
	MaxBytes       int      `json:"maxBytes"`
}

type FilesConfig struct {
	ReadOnly bool `json:"readOnly"`
}

// HTTPTool — a user-defined tool backed by an HTTP endpoint (webhook).
type HTTPTool struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Method      string            `json:"method"` // GET | POST
	URL         string            `json:"url"`    // may contain {{arg}} placeholders
	Headers     map[string]string `json:"headers"`
	Parameters  json.RawMessage   `json:"parameters"` // JSON Schema
}

// MCPServer — Model Context Protocol server whose tools join the registry.
type MCPServer struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"` // stdio | http
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
}

// SkillsSpec — skills with progressive disclosure (ctx.skills).
type SkillsSpec struct {
	Mode  string  `json:"mode"` // progressive | eager
	Items []Skill `json:"items"`
}

type Skill struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
}

// MemorySpec — long-term memory (AGENTS.md-style notes + persistent store).
type MemorySpec struct {
	StaticNotes string `json:"staticNotes"`
	Persistent  bool   `json:"persistent"`
	Path        string `json:"path"`
	Scope       string `json:"scope"`      // global | per_user
	AutoInject  int    `json:"autoInject"` // inject N most recent memories into prompt; 0 = none
	EnableTools bool   `json:"enableTools"`
}

// SessionSpec — the session log / persistence (ctx.sessions).
type SessionSpec struct {
	Mode        string `json:"mode"` // stateless | server
	Dir         string `json:"dir"`
	HeaderName  string `json:"headerName"`
	MaxMessages int    `json:"maxMessages"`
}

// ContextSpec — context-window management (ctx.compaction, ctx.toolResultPruner).
type ContextSpec struct {
	MaxContextTokens   int     `json:"maxContextTokens"`
	Strategy           string  `json:"strategy"` // none | sliding_window | summarize
	TriggerRatio       float64 `json:"triggerRatio"`
	KeepRecentMessages int     `json:"keepRecentMessages"`
	SummaryPrompt      string  `json:"summaryPrompt"`
	PruneToolResults   bool    `json:"pruneToolResults"`
	PruneKeepLast      int     `json:"pruneKeepLast"`
	PruneMaxChars      int     `json:"pruneMaxChars"`
}

// LoopSpec — the agent loop driver (ctx.agentLoop).
type LoopSpec struct {
	Strategy       string        `json:"strategy"` // react | plan_execute | single_shot
	MaxSteps       int           `json:"maxSteps"`
	MaxToolCalls   int           `json:"maxToolCalls"`
	TimeBudgetSec  int           `json:"timeBudgetSec"`
	OnToolError    string        `json:"onToolError"` // continue | stop
	OnMaxSteps     string        `json:"onMaxSteps"`  // summarize | error
	PlanningPrompt string        `json:"planningPrompt"`
	SelfCheck      SelfCheckSpec `json:"selfCheck"`
}

type SelfCheckSpec struct {
	Enabled      bool   `json:"enabled"`
	Prompt       string `json:"prompt"`
	MaxRevisions int    `json:"maxRevisions"`
}

// SubagentSpec — a named sub-agent exposed to the parent as a delegate tool (ctx.subagents).
type SubagentSpec struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	SystemPrompt string   `json:"systemPrompt"`
	Model        string   `json:"model,omitempty"` // override model name on the same provider
	Tools        []string `json:"tools"`
	MaxSteps     int      `json:"maxSteps"`
}

// PermissionSpec — approval policy and sandbox (ctx.approval, ctx.sandboxPolicy).
type PermissionSpec struct {
	DefaultPolicy   string            `json:"defaultPolicy"` // allow | deny
	ToolPolicies    map[string]string `json:"toolPolicies"`  // tool -> allow | deny | webhook
	ApprovalWebhook string            `json:"approvalWebhook"`
	WorkspaceRoot   string            `json:"workspaceRoot"`
}

// GuardrailSpec — input/output filters.
type GuardrailSpec struct {
	InputRules     []GuardRule `json:"inputRules"`
	OutputRules    []GuardRule `json:"outputRules"`
	RedactPII      bool        `json:"redactPII"`
	MaxInputChars  int         `json:"maxInputChars"`
	BlockedMessage string      `json:"blockedMessage"`
}

type GuardRule struct {
	Name    string `json:"name"`
	Type    string `json:"type"` // regex | keyword
	Pattern string `json:"pattern"`
	Action  string `json:"action"` // block | redact | warn
}

// HookSpec — lifecycle hooks (waterfall events in DeepSeek Harness).
type HookSpec struct {
	Event     string `json:"event"` // turn_start | pre_step | pre_tool | post_tool | turn_end | on_error
	Type      string `json:"type"`  // webhook | log | inject
	URL       string `json:"url,omitempty"`
	Content   string `json:"content,omitempty"`
	Blocking  bool   `json:"blocking"`
	TimeoutMs int    `json:"timeoutMs"`
}

// ObservabilitySpec — logging and tracing (ctx.sessionTelemetry).
type ObservabilitySpec struct {
	LogLevel     string `json:"logLevel"`  // debug | info | warn | error
	LogFormat    string `json:"logFormat"` // text | json
	TraceFile    string `json:"traceFile"`
	TraceHeader  bool   `json:"traceHeader"` // return X-Harness-Trace-Id
	IncludeUsage bool   `json:"includeUsage"`
}

func f64(v float64) *float64 { return &v }

// Default returns a fully populated spec with sensible defaults.
func Default() *Spec {
	return &Spec{
		Version:     SchemaVersion,
		Name:        "my-agent",
		Description: "A custom agent built with OpenHarness",
		Server: ServerSpec{
			Host: "127.0.0.1", Port: 8080, ModelID: "my-agent", AuthMode: "none",
			APIKeyEnv: "HARNESS_API_KEYS", RequestTimeout: 300,
		},
		Model: ModelSpec{
			ModelRef: ModelRef{
				Provider: "openai", BaseURL: "https://api.deepseek.com/v1",
				Model: "deepseek-chat", APIKeyEnv: "DEEPSEEK_API_KEY",
			},
			Temperature: f64(0.3), MaxTokens: 4096, TimeoutSec: 120, Retries: 2,
			Fallbacks: []ModelRef{}, ClientParamPolicy: "respect",
		},
		Prompt: PromptSpec{
			System:             "You are a helpful, precise assistant. Use tools when they help, and say so when you are unsure.",
			Sections:           []PromptSection{},
			IncludeDateTime:    true,
			IncludeToolSummary: false,
			ClientSystemPolicy: "append",
		},
		Tools: ToolsSpec{
			Builtins:          []string{"current_time", "calculator"},
			HTTPTools:         []HTTPTool{},
			MCPServers:        []MCPServer{},
			Parallel:          true,
			MaxParallel:       4,
			ResultMaxChars:    16000,
			ResultTruncation:  "head_tail",
			TimeoutSec:        60,
			ClientToolsPolicy: "passthrough",
			Shell:             ShellConfig{AllowedCommands: []string{"ls", "cat", "grep", "echo", "wc"}, TimeoutSec: 30},
			Fetch:             FetchConfig{AllowedDomains: []string{}, MaxBytes: 200000},
			Files:             FilesConfig{ReadOnly: true},
		},
		Skills: SkillsSpec{Mode: "progressive", Items: []Skill{}},
		Memory: MemorySpec{
			Persistent: false, Path: "./harness-data/memory.json", Scope: "per_user",
			AutoInject: 5, EnableTools: true,
		},
		Sessions: SessionSpec{Mode: "stateless", Dir: "./harness-data/sessions", HeaderName: "X-Session-Id", MaxMessages: 200},
		Context: ContextSpec{
			MaxContextTokens: 64000, Strategy: "sliding_window", TriggerRatio: 0.8,
			KeepRecentMessages: 12,
			SummaryPrompt:      "Summarize the conversation so far for your own future reference. Keep facts, decisions, open tasks, file paths and tool results that still matter. Be concise.",
			PruneToolResults:   true, PruneKeepLast: 6, PruneMaxChars: 2000,
		},
		Loop: LoopSpec{
			Strategy: "react", MaxSteps: 12, MaxToolCalls: 40, TimeBudgetSec: 240,
			OnToolError: "continue", OnMaxSteps: "summarize",
			PlanningPrompt: "Before acting, write a short numbered plan for how you will answer the request. Do not call tools yet.",
			SelfCheck: SelfCheckSpec{
				Enabled: false, MaxRevisions: 1,
				Prompt: "Review your draft answer against the user's request. If it is complete and correct reply with exactly APPROVED. Otherwise reply with the corrected final answer only.",
			},
		},
		Subagents: []SubagentSpec{},
		Permissions: PermissionSpec{
			DefaultPolicy: "allow", ToolPolicies: map[string]string{"shell": "deny", "write_file": "deny"},
			WorkspaceRoot: "./workspace",
		},
		Guardrails: GuardrailSpec{
			InputRules: []GuardRule{}, OutputRules: []GuardRule{}, MaxInputChars: 100000,
			BlockedMessage: "Sorry, I can't help with that request.",
		},
		Hooks:         []HookSpec{},
		Observability: ObservabilitySpec{LogLevel: "info", LogFormat: "text", TraceHeader: true, IncludeUsage: true},
	}
}

// Parse decodes a spec, layering it over defaults so partial specs work.
func Parse(data []byte) (*Spec, error) {
	s := Default()
	// Maps merge on decode; clear defaults that users are expected to fully own.
	s.Permissions.ToolPolicies = nil
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("invalid harness spec: %w", err)
	}
	if s.Permissions.ToolPolicies == nil {
		s.Permissions.ToolPolicies = map[string]string{}
	}
	return s, nil
}

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func oneOf(field, v string, allowed ...string) error {
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	return fmt.Errorf("%s: %q must be one of %s", field, v, strings.Join(allowed, ", "))
}

// Validate returns every problem found, so the UI can show them all at once.
func (s *Spec) Validate() []string {
	var errs []string
	add := func(err error) {
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	if !nameRe.MatchString(s.Name) {
		errs = append(errs, "name: use 1-64 letters, digits, '-' or '_'")
	}
	if s.Server.Port < 1 || s.Server.Port > 65535 {
		errs = append(errs, "server.port: must be 1-65535")
	}
	add(oneOf("server.authMode", s.Server.AuthMode, "none", "bearer"))
	if s.Server.AuthMode == "bearer" && s.Server.APIKeyEnv == "" {
		errs = append(errs, "server.apiKeyEnv: required when authMode is bearer")
	}
	refs := append([]ModelRef{s.Model.ModelRef}, s.Model.Fallbacks...)
	for i, r := range refs {
		label := "model"
		if i > 0 {
			label = fmt.Sprintf("model.fallbacks[%d]", i-1)
		}
		add(oneOf(label+".provider", r.Provider, "openai", "anthropic"))
		if r.Model == "" {
			errs = append(errs, label+".model: required")
		}
		if r.BaseURL == "" {
			errs = append(errs, label+".baseUrl: required")
		}
	}
	add(oneOf("model.clientParamPolicy", s.Model.ClientParamPolicy, "respect", "ignore"))
	add(oneOf("prompt.clientSystemPolicy", s.Prompt.ClientSystemPolicy, "append", "replace", "ignore"))
	add(oneOf("tools.resultTruncation", s.Tools.ResultTruncation, "head_tail", "head"))
	add(oneOf("tools.clientToolsPolicy", s.Tools.ClientToolsPolicy, "passthrough", "ignore"))
	known := map[string]bool{}
	for _, b := range BuiltinTools {
		known[b.Name] = true
	}
	for _, b := range s.Tools.Builtins {
		if !known[b] {
			errs = append(errs, fmt.Sprintf("tools.builtins: unknown tool %q", b))
		}
	}
	seen := map[string]bool{}
	for i, t := range s.Tools.HTTPTools {
		if !nameRe.MatchString(t.Name) {
			errs = append(errs, fmt.Sprintf("tools.httpTools[%d].name: invalid", i))
		}
		if seen[t.Name] {
			errs = append(errs, fmt.Sprintf("tools.httpTools[%d].name: duplicate %q", i, t.Name))
		}
		seen[t.Name] = true
		if t.URL == "" {
			errs = append(errs, fmt.Sprintf("tools.httpTools[%d].url: required", i))
		}
		if len(t.Parameters) > 0 && !json.Valid(t.Parameters) {
			errs = append(errs, fmt.Sprintf("tools.httpTools[%d].parameters: not valid JSON", i))
		}
	}
	for i, m := range s.Tools.MCPServers {
		if !nameRe.MatchString(m.Name) {
			errs = append(errs, fmt.Sprintf("tools.mcpServers[%d].name: invalid", i))
		}
		add(oneOf(fmt.Sprintf("tools.mcpServers[%d].transport", i), m.Transport, "stdio", "http"))
		if m.Transport == "stdio" && m.Command == "" {
			errs = append(errs, fmt.Sprintf("tools.mcpServers[%d].command: required for stdio", i))
		}
		if m.Transport == "http" && m.URL == "" {
			errs = append(errs, fmt.Sprintf("tools.mcpServers[%d].url: required for http", i))
		}
	}
	add(oneOf("skills.mode", s.Skills.Mode, "progressive", "eager"))
	for i, sk := range s.Skills.Items {
		if !nameRe.MatchString(sk.Name) {
			errs = append(errs, fmt.Sprintf("skills.items[%d].name: invalid", i))
		}
	}
	add(oneOf("memory.scope", s.Memory.Scope, "global", "per_user"))
	add(oneOf("sessions.mode", s.Sessions.Mode, "stateless", "server"))
	add(oneOf("context.strategy", s.Context.Strategy, "none", "sliding_window", "summarize"))
	if s.Context.TriggerRatio <= 0 || s.Context.TriggerRatio > 1 {
		errs = append(errs, "context.triggerRatio: must be in (0, 1]")
	}
	add(oneOf("loop.strategy", s.Loop.Strategy, "react", "plan_execute", "single_shot"))
	add(oneOf("loop.onToolError", s.Loop.OnToolError, "continue", "stop"))
	add(oneOf("loop.onMaxSteps", s.Loop.OnMaxSteps, "summarize", "error"))
	if s.Loop.MaxSteps < 1 {
		errs = append(errs, "loop.maxSteps: must be >= 1")
	}
	for i, sa := range s.Subagents {
		if !nameRe.MatchString(sa.Name) {
			errs = append(errs, fmt.Sprintf("subagents[%d].name: invalid", i))
		}
		if sa.SystemPrompt == "" {
			errs = append(errs, fmt.Sprintf("subagents[%d].systemPrompt: required", i))
		}
	}
	add(oneOf("permissions.defaultPolicy", s.Permissions.DefaultPolicy, "allow", "deny"))
	for tool, p := range s.Permissions.ToolPolicies {
		add(oneOf("permissions.toolPolicies."+tool, p, "allow", "deny", "webhook"))
		if p == "webhook" && s.Permissions.ApprovalWebhook == "" {
			errs = append(errs, "permissions.approvalWebhook: required when a tool uses the webhook policy")
		}
	}
	for i, r := range append(append([]GuardRule{}, s.Guardrails.InputRules...), s.Guardrails.OutputRules...) {
		add(oneOf(fmt.Sprintf("guardrails rule %d type", i), r.Type, "regex", "keyword"))
		add(oneOf(fmt.Sprintf("guardrails rule %d action", i), r.Action, "block", "redact", "warn"))
		if r.Type == "regex" {
			if _, err := regexp.Compile(r.Pattern); err != nil {
				errs = append(errs, fmt.Sprintf("guardrails rule %q: bad regex: %v", r.Name, err))
			}
		}
	}
	for i, h := range s.Hooks {
		add(oneOf(fmt.Sprintf("hooks[%d].event", i), h.Event, HookEvents...))
		add(oneOf(fmt.Sprintf("hooks[%d].type", i), h.Type, "webhook", "log", "inject"))
		if h.Type == "webhook" && h.URL == "" {
			errs = append(errs, fmt.Sprintf("hooks[%d].url: required for webhook", i))
		}
	}
	add(oneOf("observability.logLevel", s.Observability.LogLevel, "debug", "info", "warn", "error"))
	add(oneOf("observability.logFormat", s.Observability.LogFormat, "text", "json"))
	return errs
}

var HookEvents = []string{"turn_start", "pre_step", "pre_tool", "post_tool", "turn_end", "on_error"}

// BuiltinTool describes a tool shipped inside the runtime.
type BuiltinTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Risk        string `json:"risk"` // low | medium | high
}

var BuiltinTools = []BuiltinTool{
	{"current_time", "Current date and time, optionally in a given IANA time zone.", "low"},
	{"calculator", "Evaluate an arithmetic expression (+ - * / % ^, parentheses, sqrt, abs, round...).", "low"},
	{"http_fetch", "Fetch a URL over HTTP(S) and return text. Restricted by the domain allowlist.", "medium"},
	{"read_file", "Read a text file inside the workspace root.", "medium"},
	{"list_dir", "List a directory inside the workspace root.", "low"},
	{"write_file", "Create or overwrite a file inside the workspace root.", "high"},
	{"shell", "Run a command inside the workspace root. Restricted by the command allowlist.", "high"},
	{"todo", "Keep a per-turn task checklist (plan, check off, review).", "low"},
}
