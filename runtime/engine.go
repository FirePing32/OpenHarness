package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"openharness/spec"
)

// Emitter receives streamed output. Content is the answer; Activity is agent
// progress (tool calls, plans, compaction) surfaced as reasoning_content.
type Emitter interface {
	Content(string)
	Activity(string)
}

type nopEmitter struct{}

func (nopEmitter) Content(string)  {}
func (nopEmitter) Activity(string) {}

// indentEmitter forwards a sub-agent's activity, indented, and swallows its content.
type indentEmitter struct {
	parent Emitter
	prefix string
}

func (i indentEmitter) Content(string) {}
func (i indentEmitter) Activity(s string) {
	i.parent.Activity(i.prefix + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n"+i.prefix) + "\n")
}

// Engine executes a HarnessSpec. One Engine serves many concurrent turns.
type Engine struct {
	spec     *spec.Spec
	router   *Router
	registry *Registry
	memory   *MemoryStore
	sessions *SessionStore
	guard    *Guard
	hooks    *Hooks
	perms    *Permissions
	log      *Logger
	mcp      []*mcpClient
}

func NewEngine(ctx context.Context, s *spec.Spec, log *Logger) (*Engine, error) {
	if errs := s.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("invalid spec:\n  %s", strings.Join(errs, "\n  "))
	}
	e := &Engine{
		spec:     s,
		router:   NewRouter(&s.Model, log),
		registry: NewRegistry(),
		guard:    NewGuard(s.Guardrails),
		hooks:    NewHooks(s.Hooks, log),
		perms:    NewPermissions(s.Permissions),
		log:      log,
	}
	if s.Sessions.Mode == "server" {
		e.sessions = NewSessionStore(s.Sessions)
	}
	if s.Memory.Persistent {
		m, err := OpenMemory(s.Memory)
		if err != nil {
			return nil, err
		}
		e.memory = m
	}

	builtins := builtinTools(s)
	for _, name := range s.Tools.Builtins {
		e.registry.Add(builtins[name])
	}
	toolTimeout := time.Duration(s.Tools.TimeoutSec) * time.Second
	if toolTimeout <= 0 {
		toolTimeout = 60 * time.Second
	}
	for _, t := range s.Tools.HTTPTools {
		e.registry.Add(httpTool(t, toolTimeout))
	}
	for _, m := range s.Tools.MCPServers {
		e.connectMCPServer(ctx, m)
	}
	e.addSkillTools()
	e.addMemoryTools()
	for _, sa := range s.Subagents {
		e.addSubagent(sa)
	}
	return e, nil
}

func (e *Engine) connectMCPServer(ctx context.Context, m spec.MCPServer) {
	c, err := connectMCP(ctx, m, e.log)
	if err != nil {
		e.log.Warn("MCP server unavailable; continuing without it", "server", m.Name, "err", err.Error())
		return
	}
	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tools, err := c.listTools(lctx)
	if err != nil {
		e.log.Warn("MCP tools/list failed", "server", m.Name, "err", err.Error())
		c.stop()
		return
	}
	e.mcp = append(e.mcp, c)
	for _, mt := range tools {
		mt := mt
		params := string(mt.InputSchema)
		if params == "" || params == "null" {
			params = `{"type":"object","properties":{}}`
		}
		e.registry.Add(&Tool{
			Def:    makeToolDef(mcpToolName(m.Name, mt.Name), mt.Description, params),
			Source: "mcp",
			Group:  "mcp:" + m.Name,
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
				return c.callTool(ctx, mt.Name, raw)
			},
		})
	}
	e.log.Info("MCP server connected", "server", m.Name, "tools", len(tools))
}

func (e *Engine) addSkillTools() {
	sk := e.spec.Skills
	if sk.Mode != "progressive" || len(sk.Items) == 0 {
		return
	}
	byName := map[string]spec.Skill{}
	var names []string
	for _, s := range sk.Items {
		byName[s.Name] = s
		names = append(names, s.Name)
	}
	params := fmt.Sprintf(`{"type":"object","properties":{"name":{"type":"string","enum":%s}},"required":["name"]}`, mustJSON(names))
	e.registry.Add(&Tool{
		Def:    makeToolDef("load_skill", "Load the full instructions of a skill listed in the system prompt before doing a task it covers.", params),
		Source: "skill",
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var a struct{ Name string }
			if err := decodeArgs(raw, &a); err != nil {
				return "", err
			}
			s, ok := byName[a.Name]
			if !ok {
				return "", fmt.Errorf("unknown skill %q", a.Name)
			}
			return fmt.Sprintf("# Skill: %s\n\n%s", s.Name, s.Instructions), nil
		},
	})
}

func (e *Engine) addMemoryTools() {
	if e.memory == nil || !e.spec.Memory.EnableTools {
		return
	}
	e.registry.Add(&Tool{
		Def: makeToolDef("memory_save", "Save a durable fact or preference to long-term memory so it is available in future conversations.",
			`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		Source: "memory",
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct{ Text string }
			if err := decodeArgs(raw, &a); err != nil {
				return "", err
			}
			if strings.TrimSpace(a.Text) == "" {
				return "", errors.New("text is empty")
			}
			id, err := e.memory.Add(turnFrom(ctx).UserKey, a.Text)
			if err != nil {
				return "", err
			}
			return "saved as " + id, nil
		},
	})
	e.registry.Add(&Tool{
		Def: makeToolDef("memory_search", "Search long-term memory. An empty query returns the most recent memories.",
			`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer"}}}`),
		Source: "memory",
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct {
				Query string
				Limit int
			}
			if err := decodeArgs(raw, &a); err != nil {
				return "", err
			}
			if a.Limit <= 0 {
				a.Limit = 10
			}
			hits := e.memory.Search(turnFrom(ctx).UserKey, a.Query, a.Limit)
			if len(hits) == 0 {
				return "(no matching memories)", nil
			}
			var sb strings.Builder
			for _, h := range hits {
				fmt.Fprintf(&sb, "- [%s] %s\n", h.ID, h.Text)
			}
			return sb.String(), nil
		},
	})
	e.registry.Add(&Tool{
		Def: makeToolDef("memory_delete", "Delete a memory by id when it is wrong or outdated.",
			`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
		Source: "memory",
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct{ ID string }
			if err := decodeArgs(raw, &a); err != nil {
				return "", err
			}
			ok, err := e.memory.Delete(turnFrom(ctx).UserKey, a.ID)
			if err != nil {
				return "", err
			}
			if !ok {
				return "", fmt.Errorf("no memory with id %q", a.ID)
			}
			return "deleted", nil
		},
	})
}

const maxSubagentDepth = 3

func (e *Engine) addSubagent(sa spec.SubagentSpec) {
	desc := sa.Description
	if desc == "" {
		desc = "Delegate a task to the " + sa.Name + " sub-agent."
	}
	desc += " The sub-agent starts with a fresh context: include everything it needs in the task."
	e.registry.Add(&Tool{
		Def: makeToolDef("agent_"+sa.Name, desc,
			`{"type":"object","properties":{"task":{"type":"string","description":"Complete, self-contained instructions"}},"required":["task"]}`),
		Source: "subagent",
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct{ Task string }
			if err := decodeArgs(raw, &a); err != nil {
				return "", err
			}
			parent := turnFrom(ctx)
			if parent.Depth >= maxSubagentDepth {
				return "", errors.New("sub-agent nesting limit reached")
			}
			emit := parent.Emit
			if emit == nil {
				emit = nopEmitter{}
			}
			child := &turnState{
				TraceID: parent.TraceID, SessionID: parent.SessionID, UserKey: parent.UserKey,
				Depth: parent.Depth + 1, Emit: indentEmitter{emit, "  │ "},
				usage: parent.usage, usageMu: parent.usageMu,
			}
			allowed := map[string]bool{}
			for _, t := range sa.Tools {
				allowed[t] = true
			}
			steps := sa.MaxSteps
			if steps <= 0 {
				steps = 6
			}
			sys := sa.SystemPrompt
			if e.spec.Prompt.IncludeDateTime {
				sys += "\n\nCurrent date and time: " + time.Now().Format(time.RFC1123)
			}
			res, err := e.runLoop(withTurn(ctx, child), loopArgs{
				router:   e.router.WithModel(sa.Model),
				msgs:     []Message{{Role: "system", Text: sys}, {Role: "user", Text: a.Task}},
				tools:    e.registry.Defs(allowed),
				allowed:  allowed,
				maxSteps: steps,
			})
			if err != nil {
				return "", err
			}
			return res.text, nil
		},
	})
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func newID(prefix string) string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (e *Engine) Close() {
	for _, c := range e.mcp {
		c.stop()
	}
}

// Spec returns the spec the engine runs.
func (e *Engine) Spec() *spec.Spec { return e.spec }

// ---------------------------------------------------------------- turns

// TurnInput is one /v1/chat/completions request, normalized.
type TurnInput struct {
	Messages    []Message
	ClientTools []ToolDef
	ToolChoice  json.RawMessage
	Temperature *float64
	TopP        *float64
	MaxTokens   int
	SessionID   string
	User        string
	TraceID     string // optional; generated when empty
}

// TurnOutput is the final answer of a turn.
type TurnOutput struct {
	Text         string
	ToolCalls    []ToolCall // calls to client-side tools (passthrough)
	FinishReason string
	Usage        Usage
	TraceID      string
}

// LiveStreaming reports whether answer tokens can be streamed as they arrive.
// Self-check and transforming output guardrails need the full answer first.
func (e *Engine) LiveStreaming() bool {
	return !e.spec.Loop.SelfCheck.Enabled && !e.guard.Transforms()
}

func (e *Engine) RunTurn(ctx context.Context, in TurnInput, em Emitter) (*TurnOutput, error) {
	if em == nil {
		em = nopEmitter{}
	}
	userKey := in.User
	if userKey == "" {
		userKey = in.SessionID
	}
	if userKey == "" {
		userKey = "default"
	}
	if in.TraceID == "" {
		in.TraceID = newID("tr_")
	}
	ts := &turnState{TraceID: in.TraceID, SessionID: in.SessionID, UserKey: userKey, Emit: em, usage: &Usage{}, usageMu: &sync.Mutex{}}
	ctx = withTurn(ctx, ts)
	out := &TurnOutput{TraceID: ts.TraceID, FinishReason: "stop"}
	started := time.Now()
	finish := func() (*TurnOutput, error) {
		out.Usage = ts.totalUsage()
		e.hooks.Fire(ctx, "turn_end", ts, map[string]any{"text": out.Text, "finishReason": out.FinishReason})
		e.log.Event(ts.TraceID, "turn/end", map[string]any{"finishReason": out.FinishReason, "ms": time.Since(started).Milliseconds(), "usage": out.Usage})
		return out, nil
	}
	fail := func(err error) (*TurnOutput, error) {
		e.hooks.Fire(ctx, "on_error", ts, map[string]any{"error": err.Error()})
		e.log.Event(ts.TraceID, "turn/error", map[string]any{"error": err.Error()})
		return nil, err
	}

	// Split client system messages from the conversation.
	var clientSystem []string
	var conv []Message
	for _, m := range in.Messages {
		if m.Role == "system" || m.Role == "developer" {
			clientSystem = append(clientSystem, m.Text)
		} else {
			conv = append(conv, m)
		}
	}
	lastUser := -1
	for i := len(conv) - 1; i >= 0; i-- {
		if conv[i].Role == "user" {
			lastUser = i
			break
		}
	}
	lastText := ""
	if lastUser >= 0 {
		lastText = conv[lastUser].Text
	}
	e.log.Event(ts.TraceID, "turn/start", map[string]any{"session": in.SessionID, "messages": len(in.Messages)})
	if hr := e.hooks.Fire(ctx, "turn_start", ts, map[string]any{"input": lastText}); hr.Block {
		out.Text, out.FinishReason = orDefault(hr.Message, e.spec.Guardrails.BlockedMessage), "content_filter"
		em.Content(out.Text)
		return finish()
	} else if hr.Inject != "" {
		clientSystem = append(clientSystem, hr.Inject)
	}

	// Input guardrails.
	if lastUser >= 0 {
		gr := e.guard.Input(lastText)
		if gr.Blocked {
			e.log.Event(ts.TraceID, "guardrail/block", map[string]any{"stage": "input", "rule": gr.Rule})
			out.Text, out.FinishReason = e.spec.Guardrails.BlockedMessage, "content_filter"
			em.Content(out.Text)
			return finish()
		}
		if len(gr.Warnings) > 0 {
			e.log.Event(ts.TraceID, "guardrail/warn", map[string]any{"stage": "input", "rules": gr.Warnings})
		}
		if gr.Text != lastText {
			conv[lastUser].Text, conv[lastUser].Parts = gr.Text, nil
		}
	}

	// Session history.
	full, newMsgs := conv, []Message(nil)
	if e.sessions != nil && in.SessionID != "" {
		history, err := e.sessions.Load(in.SessionID)
		if err != nil {
			return fail(fmt.Errorf("load session: %w", err))
		}
		newMsgs = newMessages(history, conv)
		full = append(append([]Message{}, history...), newMsgs...)
	}

	msgs := append([]Message{{Role: "system", Text: e.systemPrompt(clientSystem, ts)}}, full...)

	// Tools for this turn: internal registry plus client passthrough tools.
	var tools []ToolDef
	clientNames := map[string]bool{}
	if e.spec.Loop.Strategy != "single_shot" {
		tools = e.registry.Defs(nil)
	}
	if e.spec.Tools.ClientToolsPolicy == "passthrough" {
		for _, t := range in.ClientTools {
			if e.registry.Get(t.Function.Name) == nil {
				tools = append(tools, t)
				clientNames[t.Function.Name] = true
			}
		}
	}
	if string(in.ToolChoice) == `"none"` {
		tools = nil
	}
	params := e.params(in)

	// Planning phase.
	if e.spec.Loop.Strategy == "plan_execute" {
		planMsgs := append(append([]Message{}, msgs...), Message{Role: "user", Text: e.spec.Loop.PlanningPrompt})
		resp, err := e.chat(ctx, e.router, ChatRequest{Messages: planMsgs, Params: params}, nil)
		if err != nil {
			return fail(err)
		}
		em.Activity("Plan:\n" + strings.TrimSpace(resp.Message.Text) + "\n\n")
		e.log.Event(ts.TraceID, "plan", map[string]any{"plan": resp.Message.Text})
		// Place the plan just before the latest user message so the request stays last.
		plan := Message{Role: "system", Text: "Your plan for the next request (follow it, adapting if you learn something new):\n" + resp.Message.Text}
		at := len(msgs)
		for i := len(msgs) - 1; i > 0; i-- {
			if msgs[i].Role == "user" {
				at = i
				break
			}
		}
		msgs = append(msgs[:at], append([]Message{plan}, msgs[at:]...)...)
	}

	maxSteps := e.spec.Loop.MaxSteps
	if e.spec.Loop.Strategy == "single_shot" {
		maxSteps = 1
	}
	live := e.LiveStreaming()
	res, err := e.runLoop(ctx, loopArgs{
		router: e.router, msgs: msgs, tools: tools, clientNames: clientNames,
		maxSteps: maxSteps, params: params, live: live, deadline: started.Add(time.Duration(e.spec.Loop.TimeBudgetSec) * time.Second),
	})
	if err != nil {
		return fail(err)
	}
	out.Text, out.ToolCalls, out.FinishReason = res.text, res.clientCalls, res.finish
	generated := res.generated

	// Self-check / revision.
	sc := e.spec.Loop.SelfCheck
	if sc.Enabled && len(out.ToolCalls) == 0 && strings.TrimSpace(out.Text) != "" {
		for i := 0; i < max(sc.MaxRevisions, 1); i++ {
			review := append(append([]Message{}, res.msgs...), Message{Role: "user", Text: sc.Prompt})
			resp, err := e.chat(ctx, e.router, ChatRequest{Messages: review, Params: params}, nil)
			if err != nil {
				e.log.Warn("self-check failed; keeping draft", "err", err.Error())
				break
			}
			verdict := strings.TrimSpace(resp.Message.Text)
			if strings.HasPrefix(strings.ToUpper(verdict), "APPROVED") || verdict == "" {
				em.Activity("Self-check: approved\n")
				break
			}
			em.Activity("Self-check: revised the answer\n")
			e.log.Event(ts.TraceID, "selfcheck/revise", map[string]any{"round": i + 1})
			out.Text = verdict
			res.msgs[len(res.msgs)-1].Text = verdict
		}
	}

	// Output guardrails.
	if out.Text != "" {
		gr := e.guard.Output(out.Text)
		if gr.Blocked {
			e.log.Event(ts.TraceID, "guardrail/block", map[string]any{"stage": "output", "rule": gr.Rule})
			out.Text, out.FinishReason = e.spec.Guardrails.BlockedMessage, "content_filter"
		} else {
			out.Text = gr.Text
		}
	}
	if !live && out.Text != "" {
		em.Content(out.Text)
	}

	// Persist the turn (the session log).
	if e.sessions != nil && in.SessionID != "" {
		if n := len(generated); n > 0 && generated[n-1].Role == "assistant" && len(generated[n-1].ToolCalls) == 0 {
			generated[n-1].Text = out.Text
		}
		if err := e.sessions.Append(in.SessionID, append(newMsgs, generated...)); err != nil {
			e.log.Warn("session append failed", "err", err.Error())
		}
	}
	return finish()
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

// newMessages works out which client messages are new relative to the stored
// session: clients may send the full transcript (like most chat UIs) or only
// the latest message.
func newMessages(history, conv []Message) []Message {
	var hu []string
	for _, m := range history {
		if m.Role == "user" {
			hu = append(hu, m.Text)
		}
	}
	if len(hu) == 0 {
		return conv
	}
	seen := 0
	for i, m := range conv {
		if m.Role != "user" {
			continue
		}
		if seen < len(hu) {
			if m.Text != hu[seen] {
				return conv // diverged: treat everything as new
			}
			seen++
			continue
		}
		return conv[i:]
	}
	if seen == len(hu) && seen > 0 {
		// Client re-sent the transcript with no new user message; take the tail.
		return nil
	}
	return conv
}

func (e *Engine) params(in TurnInput) ChatParams {
	p := ChatParams{Temperature: e.spec.Model.Temperature, TopP: e.spec.Model.TopP, MaxTokens: e.spec.Model.MaxTokens, ToolChoice: in.ToolChoice}
	if e.spec.Model.ClientParamPolicy == "respect" {
		if in.Temperature != nil {
			p.Temperature = in.Temperature
		}
		if in.TopP != nil {
			p.TopP = in.TopP
		}
		if in.MaxTokens > 0 {
			p.MaxTokens = in.MaxTokens
		}
	}
	return p
}

func (e *Engine) systemPrompt(clientSystem []string, ts *turnState) string {
	s := e.spec
	var b strings.Builder
	section := func(title, body string) {
		body = strings.TrimSpace(body)
		if body == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		if title != "" {
			b.WriteString("## " + title + "\n")
		}
		b.WriteString(body)
	}
	if s.Prompt.ClientSystemPolicy == "replace" && len(clientSystem) > 0 {
		section("", strings.Join(clientSystem, "\n\n"))
	} else {
		section("", s.Prompt.System)
	}
	for _, sec := range s.Prompt.Sections {
		if sec.Enabled {
			section(sec.Title, sec.Content)
		}
	}
	if s.Prompt.IncludeDateTime {
		section("", "Current date and time: "+time.Now().Format("Monday, 2 January 2006 15:04 MST"))
	}
	if s.Prompt.IncludeToolSummary && s.Loop.Strategy != "single_shot" {
		var lines []string
		for _, d := range e.registry.Defs(nil) {
			lines = append(lines, fmt.Sprintf("- %s: %s", d.Function.Name, d.Function.Description))
		}
		section("Available tools", strings.Join(lines, "\n"))
	}
	if len(s.Skills.Items) > 0 {
		var sb strings.Builder
		if s.Skills.Mode == "progressive" {
			sb.WriteString("Before doing a task that matches a skill below, call load_skill to read its full instructions.\n")
			for _, sk := range s.Skills.Items {
				fmt.Fprintf(&sb, "- %s: %s\n", sk.Name, sk.Description)
			}
			section("Skills", sb.String())
		} else {
			for _, sk := range s.Skills.Items {
				section("Skill: "+sk.Name, sk.Description+"\n\n"+sk.Instructions)
			}
		}
	}
	section("Notes", s.Memory.StaticNotes)
	if e.memory != nil {
		if s.Memory.AutoInject > 0 {
			var lines []string
			for _, m := range e.memory.Search(ts.UserKey, "", s.Memory.AutoInject) {
				lines = append(lines, fmt.Sprintf("- [%s] %s", m.ID, m.Text))
			}
			section("What you remember", strings.Join(lines, "\n"))
		}
		if s.Memory.EnableTools {
			section("", "Use memory_save to remember durable facts and preferences the user shares, and memory_search to recall them.")
		}
	}
	if s.Prompt.ClientSystemPolicy == "append" && len(clientSystem) > 0 {
		section("Additional instructions from the caller", strings.Join(clientSystem, "\n\n"))
	}
	return b.String()
}

// ---------------------------------------------------------------- loop

type loopArgs struct {
	router      *Router
	msgs        []Message
	tools       []ToolDef
	allowed     map[string]bool // nil = every registry tool
	clientNames map[string]bool
	maxSteps    int
	params      ChatParams
	live        bool
	deadline    time.Time
}

type loopResult struct {
	text        string
	clientCalls []ToolCall
	finish      string
	msgs        []Message
	generated   []Message
}

func (ts *turnState) addUsage(u Usage) {
	if ts.usageMu == nil {
		return
	}
	ts.usageMu.Lock()
	ts.usage.Add(u)
	ts.usageMu.Unlock()
}

func (ts *turnState) totalUsage() Usage {
	if ts.usageMu == nil {
		return Usage{}
	}
	ts.usageMu.Lock()
	defer ts.usageMu.Unlock()
	return *ts.usage
}

func (e *Engine) chat(ctx context.Context, r *Router, req ChatRequest, onDelta func(string)) (*ChatResponse, error) {
	ts := turnFrom(ctx)
	start := time.Now()
	resp, err := r.Chat(ctx, req, onDelta)
	if err != nil {
		return nil, err
	}
	ts.addUsage(resp.Usage)
	e.log.Event(ts.TraceID, "llm/response", map[string]any{
		"model": req.Model, "ms": time.Since(start).Milliseconds(), "toolCalls": len(resp.Message.ToolCalls),
		"finish": resp.FinishReason, "usage": resp.Usage, "depth": ts.Depth,
	})
	return resp, nil
}

// runLoop is the ReAct driver: model step → tool calls → results → repeat.
func (e *Engine) runLoop(ctx context.Context, a loopArgs) (*loopResult, error) {
	ts := turnFrom(ctx)
	em := ts.Emit
	if em == nil {
		em = nopEmitter{}
	}
	res := &loopResult{msgs: a.msgs, finish: "stop"}
	toolCalls := 0
	streamed := false
	onDelta := func() func(string) {
		if !a.live {
			return nil
		}
		first := true
		return func(s string) {
			if first && streamed {
				em.Content("\n\n")
			}
			first = false
			streamed = true
			em.Content(s)
		}
	}

	for step := 0; step < a.maxSteps; step++ {
		if !a.deadline.IsZero() && e.spec.Loop.TimeBudgetSec > 0 && time.Now().After(a.deadline) {
			em.Activity("Time budget exhausted\n")
			break
		}
		res.msgs = e.manageContext(ctx, res.msgs)
		if hr := e.hooks.Fire(ctx, "pre_step", ts, map[string]any{"step": step}); hr.Block {
			res.text, res.finish = orDefault(hr.Message, "Stopped by a hook."), "stop"
			return res, nil
		} else if hr.Inject != "" {
			res.msgs = append(res.msgs, Message{Role: "system", Text: hr.Inject})
		}

		resp, err := e.chat(ctx, a.router, ChatRequest{Messages: res.msgs, Tools: a.tools, Params: a.params}, onDelta())
		if err != nil {
			return nil, err
		}
		msg := resp.Message
		if len(msg.ToolCalls) == 0 {
			res.msgs = append(res.msgs, msg)
			res.generated = append(res.generated, msg)
			res.text = msg.Text
			if resp.FinishReason == "length" {
				res.finish = "length"
			}
			return res, nil
		}

		// Calls to client tools end the turn: the caller executes them.
		var clientCalls []ToolCall
		for _, tc := range msg.ToolCalls {
			if a.clientNames[tc.Function.Name] {
				clientCalls = append(clientCalls, tc)
			}
		}
		if len(clientCalls) > 0 {
			msg.ToolCalls = clientCalls
			res.msgs = append(res.msgs, msg)
			res.generated = append(res.generated, msg)
			res.text, res.clientCalls, res.finish = msg.Text, clientCalls, "tool_calls"
			return res, nil
		}

		res.msgs = append(res.msgs, msg)
		res.generated = append(res.generated, msg)
		toolCalls += len(msg.ToolCalls)
		var results []toolOutcome
		if e.spec.Loop.MaxToolCalls > 0 && toolCalls > e.spec.Loop.MaxToolCalls {
			for _, tc := range msg.ToolCalls {
				results = append(results, toolOutcome{tc.ID, "Error: tool call budget for this request is exhausted. Answer with what you have.", true})
			}
		} else {
			results = e.execTools(ctx, msg.ToolCalls, a.allowed)
		}
		for _, r := range results {
			tm := Message{Role: "tool", ToolCallID: r.id, Text: r.content}
			res.msgs = append(res.msgs, tm)
			res.generated = append(res.generated, tm)
		}
		if e.spec.Loop.OnToolError == "stop" {
			for _, r := range results {
				if r.isErr {
					res.text = "I stopped because a tool failed: " + strings.TrimPrefix(r.content, "Error: ")
					em.Content(res.text)
					return res, nil
				}
			}
		}
	}

	// Step budget exhausted.
	if e.spec.Loop.OnMaxSteps == "error" {
		return nil, fmt.Errorf("agent did not finish within %d steps", a.maxSteps)
	}
	em.Activity("Step limit reached; writing the final answer\n")
	final := append(append([]Message{}, res.msgs...), Message{Role: "user", Text: "You have reached the step limit for this request. Do not call any more tools. Reply now with your best final answer based on what you have, and mention anything left unfinished."})
	resp, err := e.chat(ctx, a.router, ChatRequest{Messages: final, Params: a.params}, onDelta())
	if err != nil {
		return nil, err
	}
	res.msgs = append(res.msgs, resp.Message)
	res.generated = append(res.generated, resp.Message)
	res.text = resp.Message.Text
	return res, nil
}

type toolOutcome struct {
	id      string
	content string
	isErr   bool
}

func (e *Engine) execTools(ctx context.Context, calls []ToolCall, allowed map[string]bool) []toolOutcome {
	out := make([]toolOutcome, len(calls))
	if !e.spec.Tools.Parallel || len(calls) == 1 {
		for i, tc := range calls {
			out[i] = e.execTool(ctx, tc, allowed)
		}
		return out
	}
	limit := e.spec.Tools.MaxParallel
	if limit <= 0 {
		limit = 4
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i, tc := range calls {
		wg.Add(1)
		go func(i int, tc ToolCall) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = e.execTool(ctx, tc, allowed)
		}(i, tc)
	}
	wg.Wait()
	return out
}

// execTool runs the guarded tool pipeline: lookup → permission → pre_tool
// hooks → execute (with timeout) → truncate → post_tool hooks.
func (e *Engine) execTool(ctx context.Context, tc ToolCall, allowed map[string]bool) (o toolOutcome) {
	ts := turnFrom(ctx)
	em := ts.Emit
	if em == nil {
		em = nopEmitter{}
	}
	name := tc.Function.Name
	args := json.RawMessage(tc.Function.Arguments)
	o.id = tc.ID
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			o.content, o.isErr = fmt.Sprintf("Error: tool panicked: %v", r), true
		}
		e.log.Event(ts.TraceID, "tool/result", map[string]any{"tool": name, "ms": time.Since(start).Milliseconds(), "error": o.isErr, "chars": len(o.content), "depth": ts.Depth})
	}()
	fail := func(msg string) toolOutcome {
		em.Activity("  ✗ " + msg + "\n")
		return toolOutcome{tc.ID, "Error: " + msg, true}
	}
	em.Activity(fmt.Sprintf("▸ %s %s\n", name, truncate(string(args), 240, "head")))
	t := e.registry.Get(name)
	if t == nil || (allowed != nil && !allowed[name]) {
		return fail(fmt.Sprintf("unknown tool %q", name))
	}
	if !json.Valid(args) {
		return fail("arguments are not valid JSON")
	}
	if ok, reason := e.perms.Check(ctx, t, args, ts); !ok {
		e.log.Event(ts.TraceID, "tool/denied", map[string]any{"tool": name, "reason": reason})
		return fail(fmt.Sprintf("permission denied for %s: %s", name, reason))
	}
	if hr := e.hooks.Fire(ctx, "pre_tool", ts, map[string]any{"tool": name, "arguments": args}); hr.Block {
		return fail("blocked by hook: " + orDefault(hr.Message, "no reason given"))
	}
	timeout := time.Duration(e.spec.Tools.TimeoutSec) * time.Second
	if timeout <= 0 || t.Source == "subagent" {
		timeout = 10 * time.Minute
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := t.Run(tctx, args)
	if err != nil {
		e.hooks.Fire(ctx, "post_tool", ts, map[string]any{"tool": name, "error": err.Error()})
		return fail(err.Error())
	}
	result = truncate(result, e.spec.Tools.ResultMaxChars, e.spec.Tools.ResultTruncation)
	e.hooks.Fire(ctx, "post_tool", ts, map[string]any{"tool": name, "result": truncate(result, 2000, "head")})
	em.Activity("  ✓ " + strings.ReplaceAll(truncate(strings.TrimSpace(result), 160, "head"), "\n", " ") + "\n")
	return toolOutcome{tc.ID, result, false}
}

// ---------------------------------------------------------------- context

// manageContext prunes stale tool results, then compacts when the estimated
// token count crosses the trigger ratio.
func (e *Engine) manageContext(ctx context.Context, msgs []Message) []Message {
	cfg := e.spec.Context
	ts := turnFrom(ctx)
	if cfg.PruneToolResults && cfg.PruneMaxChars > 0 {
		seen := 0
		var pruned []Message
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role != "tool" {
				continue
			}
			seen++
			if seen > cfg.PruneKeepLast && len(msgs[i].Text) > cfg.PruneMaxChars {
				if pruned == nil {
					pruned = append([]Message{}, msgs...)
				}
				pruned[i].Text = truncate(msgs[i].Text, cfg.PruneMaxChars, "head_tail")
			}
		}
		if pruned != nil {
			msgs = pruned
		}
	}
	if cfg.Strategy == "none" || cfg.MaxContextTokens <= 0 || len(msgs) < 2 {
		return msgs
	}
	tokens := estimateTokens(msgs)
	if float64(tokens) < cfg.TriggerRatio*float64(cfg.MaxContextTokens) {
		return msgs
	}
	sys, body := msgs[0], msgs[1:]
	keep := max(cfg.KeepRecentMessages, 2)
	if len(body) <= keep {
		return msgs
	}
	cut := len(body) - keep
	for cut < len(body) && body[cut].Role == "tool" {
		cut++
	}
	if cut >= len(body) {
		return msgs
	}
	old, recent := body[:cut], body[cut:]
	note := fmt.Sprintf("[%d earlier messages were removed to fit the context window.]", len(old))
	if cfg.Strategy == "summarize" {
		var tr strings.Builder
		for _, m := range old {
			fmt.Fprintf(&tr, "%s: %s\n", strings.ToUpper(m.Role), truncate(m.Text, 4000, "head_tail"))
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&tr, "  (called %s %s)\n", tc.Function.Name, truncate(tc.Function.Arguments, 300, "head"))
			}
		}
		resp, err := e.chat(ctx, e.router, ChatRequest{Messages: []Message{
			{Role: "system", Text: cfg.SummaryPrompt},
			{Role: "user", Text: tr.String()},
		}, Params: ChatParams{MaxTokens: 2048}}, nil)
		if err == nil && strings.TrimSpace(resp.Message.Text) != "" {
			note = "Summary of the earlier conversation:\n" + resp.Message.Text
		} else if err != nil {
			e.log.Warn("summarize compaction failed; falling back to sliding window", "err", err.Error())
		}
	}
	ts.Emit.Activity(fmt.Sprintf("Compacted context (%s): %d → %d messages\n", cfg.Strategy, len(msgs), len(recent)+2))
	e.log.Event(ts.TraceID, "compaction", map[string]any{"strategy": cfg.Strategy, "before": tokens, "dropped": len(old)})
	return append([]Message{sys, {Role: "system", Text: note}}, recent...)
}
