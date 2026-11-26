package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"openharness/spec"
)

// ---------------------------------------------------------------- guardrails

type compiledRule struct {
	spec.GuardRule
	re *regexp.Regexp
}

// Guard applies input/output rules and PII redaction.
type Guard struct {
	cfg    spec.GuardrailSpec
	input  []compiledRule
	output []compiledRule
}

var piiPatterns = []*regexp.Regexp{
	regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`),               // email
	regexp.MustCompile(`\b(?:\d[ -]?){13,16}\b`),                                       // card-like numbers
	regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),                                        // US SSN
	regexp.MustCompile(`\+?\d{1,3}[\s.-]?\(?\d{2,4}\)?[\s.-]?\d{3,4}[\s.-]?\d{3,4}\b`), // phone
}

func compileRules(rules []spec.GuardRule) []compiledRule {
	var out []compiledRule
	for _, r := range rules {
		pat := r.Pattern
		if r.Type == "keyword" {
			pat = `(?i)` + regexp.QuoteMeta(r.Pattern)
		}
		re, err := regexp.Compile(pat)
		if err != nil || r.Pattern == "" {
			continue
		}
		out = append(out, compiledRule{r, re})
	}
	return out
}

func NewGuard(cfg spec.GuardrailSpec) *Guard {
	return &Guard{cfg: cfg, input: compileRules(cfg.InputRules), output: compileRules(cfg.OutputRules)}
}

// GuardResult says whether text was blocked and carries the (possibly redacted) text.
type GuardResult struct {
	Text     string
	Blocked  bool
	Rule     string
	Warnings []string
}

func (g *Guard) apply(text string, rules []compiledRule) GuardResult {
	res := GuardResult{Text: text}
	for _, r := range rules {
		if !r.re.MatchString(res.Text) {
			continue
		}
		switch r.Action {
		case "block":
			res.Blocked, res.Rule = true, r.Name
			return res
		case "redact":
			res.Text = r.re.ReplaceAllString(res.Text, "[REDACTED]")
		case "warn":
			res.Warnings = append(res.Warnings, r.Name)
		}
	}
	if g.cfg.RedactPII {
		for _, re := range piiPatterns {
			res.Text = re.ReplaceAllString(res.Text, "[REDACTED]")
		}
	}
	return res
}

func (g *Guard) Input(text string) GuardResult {
	if g.cfg.MaxInputChars > 0 && len(text) > g.cfg.MaxInputChars {
		return GuardResult{Text: text, Blocked: true, Rule: "max_input_chars"}
	}
	return g.apply(text, g.input)
}

func (g *Guard) Output(text string) GuardResult { return g.apply(text, g.output) }

// Transforms reports whether output may change, which forces buffered streaming.
func (g *Guard) Transforms() bool { return len(g.output) > 0 || g.cfg.RedactPII }

// ---------------------------------------------------------------- hooks

// HookResult is what a hook decided.
type HookResult struct {
	Block   bool
	Message string
	Inject  string
}

type Hooks struct {
	byEvent map[string][]spec.HookSpec
	client  *http.Client
	log     *Logger
}

func NewHooks(hs []spec.HookSpec, log *Logger) *Hooks {
	h := &Hooks{byEvent: map[string][]spec.HookSpec{}, client: newHTTPClient(0), log: log}
	for _, x := range hs {
		h.byEvent[x.Event] = append(h.byEvent[x.Event], x)
	}
	return h
}

// Fire runs every hook for an event in order (a waterfall: the first block wins).
func (h *Hooks) Fire(ctx context.Context, event string, ts *turnState, data map[string]any) HookResult {
	var out HookResult
	for _, hk := range h.byEvent[event] {
		switch hk.Type {
		case "log":
			h.log.Info("hook", "event", event, "trace", ts.TraceID, "data", data)
		case "inject":
			if hk.Content != "" {
				out.Inject = strings.TrimSpace(out.Inject + "\n" + hk.Content)
			}
		case "webhook":
			payload := map[string]any{"event": event, "traceId": ts.TraceID, "sessionId": ts.SessionID, "data": data}
			if !hk.Blocking {
				go h.post(context.WithoutCancel(ctx), hk, payload)
				continue
			}
			r, err := h.post(ctx, hk, payload)
			if err != nil {
				h.log.Warn("hook failed", "event", event, "url", hk.URL, "err", err.Error())
				continue
			}
			if r.Inject != "" {
				out.Inject = strings.TrimSpace(out.Inject + "\n" + r.Inject)
			}
			if r.Block {
				out.Block, out.Message = true, r.Message
				return out
			}
		}
	}
	return out
}

func (h *Hooks) post(ctx context.Context, hk spec.HookSpec, payload map[string]any) (HookResult, error) {
	timeout := time.Duration(hk.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", hk.URL, bytes.NewReader(b))
	if err != nil {
		return HookResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return HookResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return HookResult{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var r struct {
		Action  string `json:"action"`
		Message string `json:"message"`
		Inject  string `json:"inject"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&r)
	return HookResult{Block: r.Action == "block", Message: r.Message, Inject: r.Inject}, nil
}

// ---------------------------------------------------------------- permissions

type Permissions struct {
	cfg    spec.PermissionSpec
	client *http.Client
}

func NewPermissions(cfg spec.PermissionSpec) *Permissions {
	return &Permissions{cfg: cfg, client: newHTTPClient(15 * time.Second)}
}

// Check resolves tool name → group → default policy; approvals fail closed.
func (p *Permissions) Check(ctx context.Context, t *Tool, args json.RawMessage, ts *turnState) (bool, string) {
	name := t.Def.Function.Name
	policy, ok := p.cfg.ToolPolicies[name]
	if !ok && t.Group != "" {
		policy, ok = p.cfg.ToolPolicies[t.Group]
	}
	if !ok {
		policy = p.cfg.DefaultPolicy
	}
	switch policy {
	case "allow":
		return true, ""
	case "webhook":
		if p.cfg.ApprovalWebhook == "" {
			return false, "no approval webhook configured"
		}
		b, _ := json.Marshal(map[string]any{"tool": name, "arguments": args, "sessionId": ts.SessionID, "traceId": ts.TraceID})
		req, err := http.NewRequestWithContext(ctx, "POST", p.cfg.ApprovalWebhook, bytes.NewReader(b))
		if err != nil {
			return false, err.Error()
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := p.client.Do(req)
		if err != nil {
			return false, "approval webhook unreachable"
		}
		defer resp.Body.Close()
		var r struct {
			Allow  bool   `json:"allow"`
			Reason string `json:"reason"`
		}
		if resp.StatusCode >= 400 || json.NewDecoder(resp.Body).Decode(&r) != nil {
			return false, "approval webhook returned an invalid response"
		}
		return r.Allow, r.Reason
	default:
		return false, "denied by policy"
	}
}
