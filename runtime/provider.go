package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"openharness/spec"
)

// Provider is a model adapter. onDelta (may be nil) receives streamed text.
type Provider interface {
	Name() string
	Chat(ctx context.Context, req ChatRequest, onDelta func(string)) (*ChatResponse, error)
}

type providerBase struct {
	ref     spec.ModelRef
	cfg     *spec.ModelSpec
	client  *http.Client
	headers map[string]string
}

func (p *providerBase) Name() string { return p.ref.Provider + ":" + p.ref.Model }

func (p *providerBase) apiKey() string {
	if p.ref.APIKey != "" {
		return p.ref.APIKey
	}
	if p.ref.APIKeyEnv != "" {
		return LookupEnv(p.ref.APIKeyEnv)
	}
	return ""
}

// statusError marks HTTP errors so the retry logic can tell transient ones apart.
type statusError struct {
	code int
	body string
}

func (e *statusError) Error() string { return fmt.Sprintf("model API returned %d: %s", e.code, e.body) }

func (p *providerBase) post(ctx context.Context, url string, body any, hdr map[string]string) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	for k, v := range p.headers {
		req.Header.Set(k, v)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &statusError{resp.StatusCode, strings.TrimSpace(string(data))}
	}
	return resp, nil
}

// sseEvents yields (event, data) pairs from a server-sent-events body.
func sseEvents(r io.Reader, fn func(event, data string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	var event string
	var data []string
	flush := func() error {
		if len(data) == 0 {
			event = ""
			return nil
		}
		err := fn(event, strings.Join(data, "\n"))
		event, data = "", nil
		return err
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(line[5:], " "))
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return flush()
}

var errStop = errors.New("stop")

// ---------------------------------------------------------------- OpenAI

type openAIProvider struct{ providerBase }

func (p *openAIProvider) Chat(ctx context.Context, req ChatRequest, onDelta func(string)) (*ChatResponse, error) {
	body := map[string]any{
		"model":          req.Model,
		"messages":       req.Messages,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if req.Params.Temperature != nil {
		body["temperature"] = *req.Params.Temperature
	}
	if req.Params.TopP != nil {
		body["top_p"] = *req.Params.TopP
	}
	if req.Params.MaxTokens > 0 {
		body["max_tokens"] = req.Params.MaxTokens
	}
	if p.cfg.ReasoningEffort != "" {
		body["reasoning_effort"] = p.cfg.ReasoningEffort
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
		if len(req.Params.ToolChoice) > 0 {
			body["tool_choice"] = req.Params.ToolChoice
		}
	}
	for k, v := range p.cfg.ExtraBody {
		body[k] = v
	}
	hdr := map[string]string{}
	if k := p.apiKey(); k != "" {
		hdr["Authorization"] = "Bearer " + k
	}
	resp, err := p.post(ctx, strings.TrimRight(p.ref.BaseURL, "/")+"/chat/completions", body, hdr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	out := &ChatResponse{Message: Message{Role: "assistant"}}
	var text strings.Builder
	calls := map[int]*ToolCall{}
	err = sseEvents(resp.Body, func(_, data string) error {
		if data == "[DONE]" {
			return errStop
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   *string    `json:"content"`
					ToolCalls []ToolCall `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *Usage `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil // tolerate keep-alive comments and vendor noise
		}
		if chunk.Error != nil {
			return fmt.Errorf("model stream error: %s", chunk.Error.Message)
		}
		if chunk.Usage != nil {
			out.Usage = *chunk.Usage
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content != nil && *c.Delta.Content != "" {
				text.WriteString(*c.Delta.Content)
				if onDelta != nil {
					onDelta(*c.Delta.Content)
				}
			}
			for _, tc := range c.Delta.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				cur, ok := calls[idx]
				if !ok {
					cur = &ToolCall{Type: "function"}
					calls[idx] = cur
				}
				if tc.ID != "" {
					cur.ID = tc.ID
				}
				if tc.Function.Name != "" {
					cur.Function.Name += tc.Function.Name
				}
				cur.Function.Arguments += tc.Function.Arguments
			}
			if c.FinishReason != nil {
				out.FinishReason = *c.FinishReason
			}
		}
		return nil
	})
	if err != nil && err != errStop {
		return nil, err
	}
	out.Message.Text = text.String()
	idxs := make([]int, 0, len(calls))
	for i := range calls {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	for _, i := range idxs {
		tc := *calls[i]
		if tc.ID == "" {
			tc.ID = fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), i)
		}
		if strings.TrimSpace(tc.Function.Arguments) == "" {
			tc.Function.Arguments = "{}"
		}
		out.Message.ToolCalls = append(out.Message.ToolCalls, tc)
	}
	return out, nil
}

// ---------------------------------------------------------------- Anthropic

type anthropicProvider struct{ providerBase }

type aBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type aMessage struct {
	Role    string   `json:"role"`
	Content []aBlock `json:"content"`
}

// toAnthropic converts OpenAI-style history: system messages are hoisted,
// tool calls become tool_use blocks and tool results become user tool_result blocks.
func toAnthropic(msgs []Message) (string, []aMessage) {
	var system []string
	var out []aMessage
	push := func(role string, blocks ...aBlock) {
		if len(out) > 0 && out[len(out)-1].Role == role {
			out[len(out)-1].Content = append(out[len(out)-1].Content, blocks...)
			return
		}
		out = append(out, aMessage{Role: role, Content: blocks})
	}
	for _, m := range msgs {
		switch m.Role {
		case "system", "developer":
			system = append(system, m.Text)
		case "user":
			push("user", aBlock{Type: "text", Text: nonEmpty(m.Text)})
		case "assistant":
			var blocks []aBlock
			if strings.TrimSpace(m.Text) != "" {
				blocks = append(blocks, aBlock{Type: "text", Text: m.Text})
			}
			for _, tc := range m.ToolCalls {
				in := json.RawMessage(tc.Function.Arguments)
				if !json.Valid(in) {
					in = json.RawMessage("{}")
				}
				blocks = append(blocks, aBlock{Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: in})
			}
			if len(blocks) == 0 {
				blocks = append(blocks, aBlock{Type: "text", Text: "(no content)"})
			}
			push("assistant", blocks...)
		case "tool":
			push("user", aBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: nonEmpty(m.Text)})
		}
	}
	if len(out) > 0 && out[0].Role != "user" {
		out = append([]aMessage{{Role: "user", Content: []aBlock{{Type: "text", Text: "(continuing conversation)"}}}}, out...)
	}
	return strings.Join(system, "\n\n"), out
}

func nonEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(empty)"
	}
	return s
}

func (p *anthropicProvider) Chat(ctx context.Context, req ChatRequest, onDelta func(string)) (*ChatResponse, error) {
	system, msgs := toAnthropic(req.Messages)
	maxTokens := req.Params.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	body := map[string]any{"model": req.Model, "messages": msgs, "max_tokens": maxTokens, "stream": true}
	if system != "" {
		body["system"] = system
	}
	if req.Params.Temperature != nil {
		body["temperature"] = *req.Params.Temperature
	}
	if req.Params.TopP != nil && req.Params.Temperature == nil {
		body["top_p"] = *req.Params.TopP
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			params := t.Function.Parameters
			if len(params) == 0 {
				params = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, map[string]any{"name": t.Function.Name, "description": t.Function.Description, "input_schema": params})
		}
		body["tools"] = tools
		if tc := anthropicToolChoice(req.Params.ToolChoice); tc != nil {
			body["tool_choice"] = tc
		}
	}
	for k, v := range p.cfg.ExtraBody {
		body[k] = v
	}
	hdr := map[string]string{"anthropic-version": "2023-06-01", "x-api-key": p.apiKey()}
	resp, err := p.post(ctx, strings.TrimRight(p.ref.BaseURL, "/")+"/v1/messages", body, hdr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	out := &ChatResponse{Message: Message{Role: "assistant"}}
	var text strings.Builder
	type pending struct {
		id, name string
		args     strings.Builder
	}
	blocks := map[int]*pending{}
	var order []int
	err = sseEvents(resp.Body, func(event, data string) error {
		var ev struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock aBlock `json:"content_block"`
			Delta        struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Message struct {
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil
		}
		switch ev.Type {
		case "message_start":
			out.Usage.PromptTokens = ev.Message.Usage.InputTokens
		case "content_block_start":
			if ev.ContentBlock.Type == "tool_use" {
				blocks[ev.Index] = &pending{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
				order = append(order, ev.Index)
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				text.WriteString(ev.Delta.Text)
				if onDelta != nil {
					onDelta(ev.Delta.Text)
				}
			case "input_json_delta":
				if b := blocks[ev.Index]; b != nil {
					b.args.WriteString(ev.Delta.PartialJSON)
				}
			}
		case "message_delta":
			out.Usage.CompletionTokens = ev.Usage.OutputTokens
			switch ev.Delta.StopReason {
			case "tool_use":
				out.FinishReason = "tool_calls"
			case "max_tokens":
				out.FinishReason = "length"
			case "":
			default:
				out.FinishReason = "stop"
			}
		case "message_stop":
			return errStop
		case "error":
			return fmt.Errorf("model stream error: %s", ev.Error.Message)
		}
		return nil
	})
	if err != nil && err != errStop {
		return nil, err
	}
	out.Usage.TotalTokens = out.Usage.PromptTokens + out.Usage.CompletionTokens
	out.Message.Text = text.String()
	for _, i := range order {
		b := blocks[i]
		args := b.args.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		out.Message.ToolCalls = append(out.Message.ToolCalls, newToolCall(b.id, b.name, args))
	}
	return out, nil
}

func anthropicToolChoice(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		switch s {
		case "required":
			return map[string]any{"type": "any"}
		case "auto":
			return map[string]any{"type": "auto"}
		}
		return nil // "none" is handled by not sending tools
	}
	var obj struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Function.Name != "" {
		return map[string]any{"type": "tool", "name": obj.Function.Name}
	}
	return nil
}

// ---------------------------------------------------------------- routing

func newProvider(ref spec.ModelRef, cfg *spec.ModelSpec) Provider {
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	base := providerBase{ref: ref, cfg: cfg, client: newHTTPClient(timeout), headers: cfg.ExtraHeaders}
	if ref.Provider == "anthropic" {
		return &anthropicProvider{base}
	}
	return &openAIProvider{base}
}

// modelRoute is one entry of the fallback chain.
type modelRoute struct {
	provider Provider
	model    string
}

// Router tries the primary model with retries, then each fallback in order.
type Router struct {
	routes  []modelRoute
	retries int
	log     *Logger
}

func NewRouter(cfg *spec.ModelSpec, log *Logger) *Router {
	r := &Router{retries: cfg.Retries, log: log}
	for _, ref := range append([]spec.ModelRef{cfg.ModelRef}, cfg.Fallbacks...) {
		r.routes = append(r.routes, modelRoute{newProvider(ref, cfg), ref.Model})
	}
	return r
}

// WithModel returns a router whose primary model name is overridden (sub-agents).
func (r *Router) WithModel(model string) *Router {
	if model == "" {
		return r
	}
	cp := *r
	cp.routes = append([]modelRoute{{r.routes[0].provider, model}}, r.routes[1:]...)
	return &cp
}

func retryable(err error) bool {
	var se *statusError
	if errors.As(err, &se) {
		return se.code == 429 || se.code >= 500
	}
	return !errors.Is(err, context.Canceled)
}

func (r *Router) Chat(ctx context.Context, req ChatRequest, onDelta func(string)) (*ChatResponse, error) {
	var lastErr error
	for i, route := range r.routes {
		req.Model = route.model
		for attempt := 0; attempt <= r.retries; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(attempt*attempt) * 500 * time.Millisecond):
				}
			}
			resp, err := route.provider.Chat(ctx, req, onDelta)
			if err == nil {
				resp.Model = route.model
				return resp, nil
			}
			lastErr = err
			r.log.Warn("model call failed", "route", i, "model", route.model, "attempt", attempt+1, "err", err.Error())
			if !retryable(err) || ctx.Err() != nil {
				break
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}
