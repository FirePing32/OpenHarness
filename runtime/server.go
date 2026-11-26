package runtime

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"openharness/spec"
)

// Server exposes an Engine as an OpenAI-compatible HTTP API.
type Server struct {
	engine  *Engine
	spec    *spec.Spec
	log     *Logger
	created int64
}

func NewServer(ctx context.Context, s *spec.Spec) (*Server, error) {
	log := NewLogger(s.Observability)
	e, err := NewEngine(ctx, s, log)
	if err != nil {
		log.Close()
		return nil, err
	}
	return &Server{engine: e, spec: s, log: log, created: time.Now().Unix()}, nil
}

func (s *Server) Close() {
	s.engine.Close()
	s.log.Close()
}

func (s *Server) Logger() *Logger { return s.log }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "agent": s.spec.Name})
	})
	mux.HandleFunc("GET /v1/models", s.auth(s.handleModels))
	mux.HandleFunc("GET /v1/models/{id}", s.auth(s.handleModels))
	mux.HandleFunc("POST /v1/chat/completions", s.auth(s.handleChat))
	mux.HandleFunc("GET /v1/harness", s.auth(s.handleHarness))
	return s.cors(mux)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, code int, typ, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]any{"message": msg, "type": typ, "code": nil}})
}

func (s *Server) cors(next http.Handler) http.Handler {
	origins := s.spec.Server.CORSOrigins
	if len(origins) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (contains(origins, "*") || contains(origins, origin)) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, "+s.spec.Sessions.HeaderName)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Expose-Headers", "X-Harness-Trace-Id")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	if s.spec.Server.AuthMode != "bearer" {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var keys []string
		for _, k := range strings.Split(LookupEnv(s.spec.Server.APIKeyEnv), ",") {
			if k = strings.TrimSpace(k); k != "" {
				keys = append(keys, k)
			}
		}
		if len(keys) == 0 {
			apiError(w, 500, "server_error", "server auth is enabled but "+s.spec.Server.APIKeyEnv+" is not set")
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		for _, k := range keys {
			if subtle.ConstantTimeCompare([]byte(got), []byte(k)) == 1 {
				next(w, r)
				return
			}
		}
		apiError(w, 401, "invalid_request_error", "invalid API key")
	}
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	m := map[string]any{"id": s.spec.Server.ModelID, "object": "model", "created": s.created, "owned_by": "openharness"}
	if r.PathValue("id") != "" {
		if r.PathValue("id") != s.spec.Server.ModelID {
			apiError(w, 404, "invalid_request_error", "model not found")
			return
		}
		writeJSON(w, 200, m)
		return
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": []any{m}})
}

// handleHarness describes the running harness with secrets stripped.
func (s *Server) handleHarness(w http.ResponseWriter, r *http.Request) {
	cp := *s.spec
	cp.Model.APIKey = ""
	fb := make([]spec.ModelRef, len(cp.Model.Fallbacks))
	for i, f := range cp.Model.Fallbacks {
		f.APIKey = ""
		fb[i] = f
	}
	cp.Model.Fallbacks = fb
	var tools []string
	for _, d := range s.engine.registry.Defs(nil) {
		tools = append(tools, d.Function.Name)
	}
	writeJSON(w, 200, map[string]any{"spec": cp, "tools": tools})
}

type chatRequest struct {
	Model         string    `json:"model"`
	Messages      []Message `json:"messages"`
	Stream        bool      `json:"stream"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
	Tools               []ToolDef       `json:"tools"`
	ToolChoice          json.RawMessage `json:"tool_choice"`
	Temperature         *float64        `json:"temperature"`
	TopP                *float64        `json:"top_p"`
	MaxTokens           int             `json:"max_tokens"`
	MaxCompletionTokens int             `json:"max_completion_tokens"`
	User                string          `json:"user"`
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&req); err != nil {
		apiError(w, 400, "invalid_request_error", "invalid JSON body: "+err.Error())
		return
	}
	if len(req.Messages) == 0 {
		apiError(w, 400, "invalid_request_error", "messages must not be empty")
		return
	}
	maxTokens := req.MaxTokens
	if req.MaxCompletionTokens > 0 {
		maxTokens = req.MaxCompletionTokens
	}
	in := TurnInput{
		Messages: req.Messages, ClientTools: req.Tools, ToolChoice: req.ToolChoice,
		Temperature: req.Temperature, TopP: req.TopP, MaxTokens: maxTokens,
		SessionID: r.Header.Get(s.spec.Sessions.HeaderName), User: req.User,
		TraceID: newID("tr_"),
	}
	if s.spec.Observability.TraceHeader {
		w.Header().Set("X-Harness-Trace-Id", in.TraceID)
	}
	if in.SessionID == "" && s.spec.Sessions.Mode == "server" {
		in.SessionID = req.User
	}
	ctx := r.Context()
	if t := s.spec.Server.RequestTimeout; t > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(t)*time.Second)
		defer cancel()
	}
	id := newID("chatcmpl-")
	created := time.Now().Unix()
	model := s.spec.Server.ModelID

	if !req.Stream {
		out, err := s.engine.RunTurn(ctx, in, nil)
		if err != nil {
			s.log.Error("turn failed", "err", err.Error())
			apiError(w, 502, "server_error", err.Error())
			return
		}
		msg := map[string]any{"role": "assistant", "content": out.Text}
		if len(out.ToolCalls) > 0 {
			msg["tool_calls"] = out.ToolCalls
			if out.Text == "" {
				msg["content"] = nil
			}
		}
		resp := map[string]any{
			"id": id, "object": "chat.completion", "created": created, "model": model,
			"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": out.FinishReason}},
		}
		if s.spec.Observability.IncludeUsage {
			resp["usage"] = out.Usage
		}
		writeJSON(w, 200, resp)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		apiError(w, 500, "server_error", "streaming unsupported")
		return
	}
	sse := &sseEmitter{w: w, flusher: flusher, id: id, created: created, model: model, activity: s.spec.Server.StreamToolEvents}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	sse.chunk(map[string]any{"role": "assistant", "content": ""}, nil)

	done := make(chan struct{})
	go sse.keepAlive(done)
	out, err := s.engine.RunTurn(ctx, in, sse)
	close(done)
	if err != nil {
		s.log.Error("turn failed", "err", err.Error())
		sse.write(map[string]any{"error": map[string]any{"message": err.Error(), "type": "server_error"}})
		sse.done()
		return
	}
	if len(out.ToolCalls) > 0 {
		calls := make([]map[string]any, len(out.ToolCalls))
		for i, tc := range out.ToolCalls {
			calls[i] = map[string]any{"index": i, "id": tc.ID, "type": "function",
				"function": map[string]any{"name": tc.Function.Name, "arguments": tc.Function.Arguments}}
		}
		sse.chunk(map[string]any{"tool_calls": calls}, nil)
	}
	reason := out.FinishReason
	sse.chunkWith(map[string]any{}, &reason, map[string]any{"harness_trace_id": out.TraceID})
	if s.spec.Observability.IncludeUsage && req.StreamOptions != nil && req.StreamOptions.IncludeUsage {
		sse.write(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{}, "usage": out.Usage})
	}
	sse.done()
}

// sseEmitter streams chat.completion.chunk events.
type sseEmitter struct {
	mu       sync.Mutex
	w        http.ResponseWriter
	flusher  http.Flusher
	id       string
	created  int64
	model    string
	activity bool
}

func (e *sseEmitter) write(v any) {
	b, _ := json.Marshal(v)
	e.mu.Lock()
	defer e.mu.Unlock()
	fmt.Fprintf(e.w, "data: %s\n\n", b)
	e.flusher.Flush()
}

func (e *sseEmitter) chunk(delta map[string]any, finish *string) { e.chunkWith(delta, finish, nil) }

func (e *sseEmitter) chunkWith(delta map[string]any, finish *string, extra map[string]any) {
	var fr any
	if finish != nil {
		fr = *finish
	}
	c := map[string]any{
		"id": e.id, "object": "chat.completion.chunk", "created": e.created, "model": e.model,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": fr}},
	}
	for k, v := range extra {
		c[k] = v
	}
	e.write(c)
}

func (e *sseEmitter) Content(s string) {
	if s != "" {
		e.chunk(map[string]any{"content": s}, nil)
	}
}

func (e *sseEmitter) Activity(s string) {
	if e.activity && s != "" {
		e.chunk(map[string]any{"reasoning_content": s}, nil)
	}
}

func (e *sseEmitter) done() {
	e.mu.Lock()
	defer e.mu.Unlock()
	fmt.Fprint(e.w, "data: [DONE]\n\n")
	e.flusher.Flush()
}

// keepAlive sends SSE comments so proxies don't drop long tool-heavy turns.
func (e *sseEmitter) keepAlive(done chan struct{}) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			e.mu.Lock()
			fmt.Fprint(e.w, ": keep-alive\n\n")
			e.flusher.Flush()
			e.mu.Unlock()
		}
	}
}
