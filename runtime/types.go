package runtime

import (
	"encoding/json"
	"strings"
)

// ToolCall is the OpenAI wire shape of a model-requested tool invocation.
type ToolCall struct {
	Index    *int   `json:"index,omitempty"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func newToolCall(id, name, args string) ToolCall {
	tc := ToolCall{ID: id, Type: "function"}
	tc.Function.Name = name
	tc.Function.Arguments = args
	return tc
}

// Message is the harness-internal message. It round-trips through the OpenAI
// wire format: Text holds plain text, Parts keeps multimodal array content.
type Message struct {
	Role       string
	Text       string
	Parts      json.RawMessage
	Name       string
	ToolCalls  []ToolCall
	ToolCallID string
}

type wireMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

func (m Message) MarshalJSON() ([]byte, error) {
	w := wireMessage{Role: m.Role, Name: m.Name, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID}
	switch {
	case len(m.Parts) > 0:
		w.Content = m.Parts
	case m.Text == "" && len(m.ToolCalls) > 0:
		w.Content = json.RawMessage("null")
	default:
		b, _ := json.Marshal(m.Text)
		w.Content = b
	}
	for i := range w.ToolCalls {
		w.ToolCalls[i].Index = nil
	}
	return json.Marshal(w)
}

func (m *Message) UnmarshalJSON(b []byte) error {
	var w wireMessage
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*m = Message{Role: w.Role, Name: w.Name, ToolCalls: w.ToolCalls, ToolCallID: w.ToolCallID}
	c := strings.TrimSpace(string(w.Content))
	switch {
	case c == "" || c == "null":
	case strings.HasPrefix(c, `"`):
		_ = json.Unmarshal(w.Content, &m.Text)
	case strings.HasPrefix(c, "["):
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(w.Content, &parts); err != nil {
			return err
		}
		var texts []string
		multimodal := false
		for _, p := range parts {
			if p.Type == "text" {
				texts = append(texts, p.Text)
			} else {
				multimodal = true
			}
		}
		m.Text = strings.Join(texts, "\n")
		if multimodal {
			m.Parts = append(json.RawMessage(nil), w.Content...)
		}
	}
	return nil
}

// ToolDef is a function tool definition in OpenAI format.
type ToolDef struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

func makeToolDef(name, desc, params string) ToolDef {
	d := ToolDef{Type: "function"}
	d.Function.Name = name
	d.Function.Description = desc
	if params == "" {
		params = `{"type":"object","properties":{}}`
	}
	d.Function.Parameters = json.RawMessage(params)
	return d
}

// Usage mirrors OpenAI usage accounting.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (u *Usage) Add(o Usage) {
	u.PromptTokens += o.PromptTokens
	u.CompletionTokens += o.CompletionTokens
	u.TotalTokens += o.TotalTokens
}

// ChatParams are per-call sampling parameters.
type ChatParams struct {
	Temperature *float64
	TopP        *float64
	MaxTokens   int
	ToolChoice  json.RawMessage
}

// ChatRequest is one model call.
type ChatRequest struct {
	Model    string
	Messages []Message
	Tools    []ToolDef
	Params   ChatParams
}

// ChatResponse is the assembled result of one model call.
type ChatResponse struct {
	Message      Message
	FinishReason string
	Usage        Usage
}

// estimateTokens is a cheap chars/4 heuristic used for compaction decisions.
func estimateTokens(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		n += 4 + len(m.Text)/4 + len(m.Parts)/4
		for _, tc := range m.ToolCalls {
			n += 8 + (len(tc.Function.Name)+len(tc.Function.Arguments))/4
		}
	}
	return n
}
