package runtime

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"openharness/spec"
)

// mockOpenAI replies with a calculator tool call first, then a final answer
// that echoes the last tool result. It records every request body.
type mockOpenAI struct {
	mu   sync.Mutex
	reqs []map[string]any
}

func (m *mockOpenAI) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		m.mu.Lock()
		m.reqs = append(m.reqs, body)
		m.mu.Unlock()
		msgs := body["messages"].([]any)
		last := msgs[len(msgs)-1].(map[string]any)
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(v string) { fmt.Fprintf(w, "data: %s\n\n", v) }
		_, hasTools := body["tools"]
		switch {
		case last["role"] == "tool":
			send(fmt.Sprintf(`{"choices":[{"delta":{"content":"The answer is "}}]}`))
			b, _ := json.Marshal(last["content"])
			send(fmt.Sprintf(`{"choices":[{"delta":{"content":%s},"finish_reason":"stop"}]}`, b))
		case hasTools && strings.Contains(fmt.Sprint(last["content"]), "compute"):
			send(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"calculator","arguments":""}}]}}]}`)
			send(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"expression\":\"6*7\"}"}}]},"finish_reason":"tool_calls"}]}`)
		case hasTools && strings.Contains(fmt.Sprint(last["content"]), "weather"):
			send(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"w1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}]}`)
		default:
			send(`{"choices":[{"delta":{"content":"hello from mock"},"finish_reason":"stop"}]}`)
		}
		send(`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
		send("[DONE]")
	}
}

func testServer(t *testing.T, mutate func(*spec.Spec)) (*httptest.Server, *mockOpenAI) {
	t.Helper()
	mock := &mockOpenAI{}
	llm := httptest.NewServer(mock.handler(t))
	t.Cleanup(llm.Close)
	s := spec.Default()
	s.Model.BaseURL = llm.URL
	s.Model.APIKeyEnv = ""
	s.Permissions.WorkspaceRoot = t.TempDir()
	s.Sessions.Dir = t.TempDir()
	s.Memory.Path = t.TempDir() + "/mem.json"
	s.Observability.LogLevel = "error"
	if mutate != nil {
		mutate(s)
	}
	srv, err := NewServer(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	h := httptest.NewServer(srv.Handler())
	t.Cleanup(h.Close)
	return h, mock
}

func post(t *testing.T, url, body string, hdr map[string]string) *http.Response {
	req, _ := http.NewRequest("POST", url+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestToolLoopNonStreaming(t *testing.T) {
	h, mock := testServer(t, nil)
	resp := post(t, h.URL, `{"model":"x","messages":[{"role":"user","content":"please compute 6*7"}]}`, nil)
	defer resp.Body.Close()
	var out struct {
		Choices []struct {
			Message      map[string]any `json:"message"`
			FinishReason string         `json:"finish_reason"`
		}
		Usage Usage
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if got := out.Choices[0].Message["content"]; got != "The answer is 42" {
		t.Fatalf("content = %v", got)
	}
	if out.Usage.TotalTokens != 30 {
		t.Fatalf("usage = %+v, want summed over 2 calls", out.Usage)
	}
	if resp.Header.Get("X-Harness-Trace-Id") == "" {
		t.Fatal("missing trace header")
	}
	sys := mock.reqs[0]["messages"].([]any)[0].(map[string]any)
	if sys["role"] != "system" || !strings.Contains(sys["content"].(string), "helpful") {
		t.Fatalf("system prompt not assembled: %v", sys)
	}
}

func TestStreamingWithActivity(t *testing.T) {
	h, _ := testServer(t, func(s *spec.Spec) { s.Server.StreamToolEvents = true })
	resp := post(t, h.URL, `{"model":"x","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"compute it"}]}`, nil)
	defer resp.Body.Close()
	var content, activity strings.Builder
	sawDone, sawUsage := false, false
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := line[6:]
		if data == "[DONE]" {
			sawDone = true
			continue
		}
		var c struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					Reasoning string `json:"reasoning_content"`
				}
			}
			Usage *Usage
		}
		json.Unmarshal([]byte(data), &c)
		if c.Usage != nil {
			sawUsage = true
		}
		for _, ch := range c.Choices {
			content.WriteString(ch.Delta.Content)
			activity.WriteString(ch.Delta.Reasoning)
		}
	}
	if content.String() != "The answer is 42" || !sawDone || !sawUsage {
		t.Fatalf("content=%q done=%v usage=%v", content.String(), sawDone, sawUsage)
	}
	if !strings.Contains(activity.String(), "calculator") {
		t.Fatalf("activity missing tool call: %q", activity.String())
	}
}

func TestClientToolPassthrough(t *testing.T) {
	h, _ := testServer(t, nil)
	body := `{"model":"x","messages":[{"role":"user","content":"weather in Paris?"}],
	  "tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]}`
	resp := post(t, h.URL, body, nil)
	defer resp.Body.Close()
	var out struct {
		Choices []struct {
			Message struct {
				ToolCalls []ToolCall `json:"tool_calls"`
			}
			FinishReason string `json:"finish_reason"`
		}
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Choices[0].FinishReason != "tool_calls" || out.Choices[0].Message.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("expected client tool call, got %+v", out)
	}
}

func TestPermissionDenied(t *testing.T) {
	h, _ := testServer(t, func(s *spec.Spec) { s.Permissions.ToolPolicies = map[string]string{"calculator": "deny"} })
	resp := post(t, h.URL, `{"model":"x","messages":[{"role":"user","content":"compute"}]}`, nil)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "permission denied") {
		t.Fatalf("expected denial to reach the model, got %s", b)
	}
}

func TestGuardrailBlockAndAuth(t *testing.T) {
	t.Setenv("TEST_KEYS", "k1,k2")
	h, mock := testServer(t, func(s *spec.Spec) {
		s.Server.AuthMode, s.Server.APIKeyEnv = "bearer", "TEST_KEYS"
		s.Guardrails.InputRules = []spec.GuardRule{{Name: "no-secrets", Type: "keyword", Pattern: "password", Action: "block"}}
	})
	if resp := post(t, h.URL, `{"messages":[{"role":"user","content":"hi"}]}`, nil); resp.StatusCode != 401 {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
	resp := post(t, h.URL, `{"messages":[{"role":"user","content":"my Password is x"}]}`, map[string]string{"Authorization": "Bearer k2"})
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "content_filter") || len(mock.reqs) != 0 {
		t.Fatalf("expected block without model call: %s (calls=%d)", b, len(mock.reqs))
	}
}

func TestServerSessions(t *testing.T) {
	h, mock := testServer(t, func(s *spec.Spec) { s.Sessions.Mode = "server" })
	hdr := map[string]string{"X-Session-Id": "abc"}
	post(t, h.URL, `{"messages":[{"role":"user","content":"first"}]}`, hdr).Body.Close()
	post(t, h.URL, `{"messages":[{"role":"user","content":"second"}]}`, hdr).Body.Close()
	// Full-transcript clients must not duplicate history.
	post(t, h.URL, `{"messages":[{"role":"user","content":"first"},{"role":"assistant","content":"hello from mock"},{"role":"user","content":"second"},{"role":"assistant","content":"hello from mock"},{"role":"user","content":"third"}]}`, hdr).Body.Close()
	msgs := mock.reqs[2]["messages"].([]any)
	var users []string
	for _, m := range msgs {
		mm := m.(map[string]any)
		if mm["role"] == "user" {
			users = append(users, mm["content"].(string))
		}
	}
	if strings.Join(users, ",") != "first,second,third" {
		t.Fatalf("history = %v", users)
	}
}

func TestSlidingWindowCompaction(t *testing.T) {
	h, mock := testServer(t, func(s *spec.Spec) {
		s.Context.MaxContextTokens, s.Context.KeepRecentMessages = 50, 2
	})
	var msgs []string
	for i := 0; i < 10; i++ {
		msgs = append(msgs, fmt.Sprintf(`{"role":"user","content":"%s"}`, strings.Repeat("word ", 40)))
		msgs = append(msgs, `{"role":"assistant","content":"ok"}`)
	}
	msgs = append(msgs, `{"role":"user","content":"latest"}`)
	post(t, h.URL, `{"messages":[`+strings.Join(msgs, ",")+`]}`, nil).Body.Close()
	sent := mock.reqs[0]["messages"].([]any)
	if len(sent) != 4 { // system, compaction note, 2 recent
		t.Fatalf("expected compacted history of 4, got %d", len(sent))
	}
}

func TestAnthropicConversion(t *testing.T) {
	sys, msgs := toAnthropic([]Message{
		{Role: "system", Text: "be nice"},
		{Role: "assistant", Text: "hi"},
		{Role: "user", Text: "q"},
		{Role: "assistant", ToolCalls: []ToolCall{newToolCall("t1", "calc", `{"a":1}`)}},
		{Role: "tool", ToolCallID: "t1", Text: "1"},
		{Role: "tool", ToolCallID: "t2", Text: "2"},
	})
	if sys != "be nice" || msgs[0].Role != "user" || len(msgs) != 5 {
		t.Fatalf("sys=%q msgs=%+v", sys, msgs)
	}
	if last := msgs[4]; last.Role != "user" || len(last.Content) != 2 || last.Content[0].Type != "tool_result" {
		t.Fatalf("tool results not merged: %+v", last)
	}
}

func TestCalculator(t *testing.T) {
	for expr, want := range map[string]float64{"2+3*4": 14, "(2+3)*4": 20, "2^3^2": 512, "-sqrt(16)+10 % 4": -2, "1e3/4": 250} {
		got, err := evalExpr(expr)
		if err != nil || got != want {
			t.Errorf("%s = %v (%v), want %v", expr, got, err, want)
		}
	}
	if _, err := evalExpr("2+"); err == nil {
		t.Error("expected error")
	}
}

func TestWorkspaceEscape(t *testing.T) {
	ws := workspace{t.TempDir()}
	if _, err := ws.resolve("../../etc/passwd"); err == nil {
		t.Fatal("escape not blocked")
	}
}

func TestPlanExecuteKeepsUserLast(t *testing.T) {
	h, mock := testServer(t, func(s *spec.Spec) { s.Loop.Strategy = "plan_execute" })
	post(t, h.URL, `{"messages":[{"role":"user","content":"please compute"}]}`, nil).Body.Close()
	if len(mock.reqs) != 3 { // plan, tool call, final
		t.Fatalf("expected 3 model calls, got %d", len(mock.reqs))
	}
	msgs := mock.reqs[1]["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	prev := msgs[len(msgs)-2].(map[string]any)
	if last["role"] != "user" || prev["role"] != "system" || !strings.Contains(prev["content"].(string), "plan") {
		t.Fatalf("plan placement wrong: %v / %v", prev, last)
	}
}

func TestServerSessionsRepeatedMessage(t *testing.T) {
	h, mock := testServer(t, func(s *spec.Spec) { s.Sessions.Mode = "server" })
	hdr := map[string]string{"X-Session-Id": "rep"}
	post(t, h.URL, `{"messages":[{"role":"user","content":"continue"}]}`, hdr).Body.Close()
	post(t, h.URL, `{"messages":[{"role":"user","content":"continue"}]}`, hdr).Body.Close()
	msgs := mock.reqs[1]["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "user" || len(msgs) != 4 { // system, user, assistant, user
		t.Fatalf("repeated message dropped: %d messages, last=%v", len(msgs), last)
	}
}

func TestTurnStartInjectIgnoresClientPolicy(t *testing.T) {
	h, mock := testServer(t, func(s *spec.Spec) {
		s.Prompt.ClientSystemPolicy = "ignore"
		s.Hooks = []spec.HookSpec{{Event: "turn_start", Type: "inject", Content: "HOOK-CONTEXT"}}
	})
	post(t, h.URL, `{"messages":[{"role":"system","content":"CLIENT"},{"role":"user","content":"hi"}]}`, nil).Body.Close()
	sys := mock.reqs[0]["messages"].([]any)[0].(map[string]any)["content"].(string)
	if !strings.Contains(sys, "## Context from hooks\nHOOK-CONTEXT") || strings.Contains(sys, "CLIENT") || !strings.Contains(sys, "helpful") {
		t.Fatalf("system prompt:\n%s", sys)
	}
}

func TestRedactPII(t *testing.T) {
	cases := map[string]string{
		"mail a.b+x@ex-ample.co.uk now":            "mail [REDACTED] now",
		"card 4111 1111 1111 1111 ssn 123-45-6789": "card [REDACTED] ssn [REDACTED]",
		"card 5555-5555-5555-4444.":                "card [REDACTED].",
		"call +1 415 555 0100 or (415) 555-0100":   "call [REDACTED] or [REDACTED]",
		"office 415-555-0100, uk +44 20 7946 0958": "office [REDACTED], uk [REDACTED]",
		"pi is 3.14159265358979":                   "pi is 3.14159265358979",
		"order 1234567890123 shipped":              "order 1234567890123 shipped",
		"year 2026 and 1500 units":                 "year 2026 and 1500 units",
	}
	for in, want := range cases {
		if got := redactPII(in); got != want {
			t.Errorf("redactPII(%q) = %q, want %q", in, got, want)
		}
	}
}
