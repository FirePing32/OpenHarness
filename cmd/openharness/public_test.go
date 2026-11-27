package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"openharness/spec"
)

func TestSandboxPreview(t *testing.T) {
	s := spec.Presets()[2].Spec // coding agent: shell + files
	s.Tools.MCPServers = []spec.MCPServer{{Name: "local", Transport: "stdio", Command: "rm"}, {Name: "remote", Transport: "http", URL: "https://x"}}
	s.Model.Fallbacks = []spec.ModelRef{{Provider: "openai", BaseURL: "https://y", Model: "m", APIKeyEnv: "SECRET"}}
	s.Server.AuthMode = "bearer"
	s.Memory.Path = "/etc/passwd"
	notes := sandboxPreview(s, "/tmp/p1", "sk-visitor")

	for _, b := range s.Tools.Builtins {
		if b == "shell" {
			t.Fatal("shell survived")
		}
	}
	if len(s.Tools.MCPServers) != 1 || s.Tools.MCPServers[0].Name != "remote" {
		t.Fatalf("mcp = %+v", s.Tools.MCPServers)
	}
	if s.Model.APIKeyEnv != "" || s.Model.APIKey != "sk-visitor" || s.Model.Fallbacks[0].APIKeyEnv != "" || s.Model.Fallbacks[0].APIKey != "sk-visitor" {
		t.Fatalf("keys not sandboxed: %+v %+v", s.Model.ModelRef, s.Model.Fallbacks[0])
	}
	for _, p := range []string{s.Memory.Path, s.Sessions.Dir, s.Permissions.WorkspaceRoot} {
		if !strings.HasPrefix(p, "/tmp/p1/") {
			t.Fatalf("path escaped sandbox: %s", p)
		}
	}
	if s.Server.AuthMode != "none" || len(notes) != 2 {
		t.Fatalf("auth=%s notes=%v", s.Server.AuthMode, notes)
	}
	if errs := s.Validate(); len(errs) > 0 {
		t.Fatalf("sandboxed spec invalid: %v", errs)
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(2, time.Hour)
	if ok, _ := l.allow("a"); !ok {
		t.Fatal("1st")
	}
	if ok, _ := l.allow("a"); !ok {
		t.Fatal("2nd")
	}
	if ok, wait := l.allow("a"); ok || wait <= 0 {
		t.Fatal("3rd should be limited")
	}
	if ok, _ := l.allow("b"); !ok {
		t.Fatal("other IP should be independent")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.2:5555"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 1.2.3.4")
	if got := clientIP(r, false); got != "10.0.0.2" {
		t.Fatalf("untrusted: %s", got)
	}
	if got := clientIP(r, true); got != "1.2.3.4" {
		t.Fatalf("trusted: %s", got)
	}
}

func TestIndexLinkPreview(t *testing.T) {
	get := func(a *app, mutate func(*http.Request)) string {
		r := httptest.NewRequest("GET", "http://harness.example.com/", nil)
		if mutate != nil {
			mutate(r)
		}
		w := httptest.NewRecorder()
		a.index(w, r)
		return w.Body.String()
	}
	local := &app{}
	if body := get(local, nil); !strings.Contains(body, `content="http://harness.example.com/og.png"`) || strings.Contains(body, "__BASE_URL__") {
		t.Fatalf("plain request: og:image not absolute")
	}
	proxied := &app{cfg: config{trustProxy: true}}
	body := get(proxied, func(r *http.Request) {
		r.Host = "internal:7070"
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Host", "harness.example.com")
	})
	if !strings.Contains(body, `content="https://harness.example.com/og.png"`) {
		t.Fatalf("behind proxy: want https public host")
	}
	// Without trust, forwarded headers are ignored.
	if body := get(local, func(r *http.Request) { r.Header.Set("X-Forwarded-Host", "evil.example") }); strings.Contains(body, "evil.example") {
		t.Fatal("untrusted X-Forwarded-Host was used")
	}
	// A hostile Host header can't inject markup.
	if body := get(local, func(r *http.Request) { r.Host = `x"><script>` }); strings.Contains(body, "<script>\"") || strings.Contains(body, `x"><script>`) {
		t.Fatal("Host header injected into the page")
	}
	t.Setenv("OPENHARNESS_PUBLIC_URL", "https://pinned.example/")
	if body := get(proxied, nil); !strings.Contains(body, `content="https://pinned.example/og.png"`) {
		t.Fatal("OPENHARNESS_PUBLIC_URL not used")
	}
}
