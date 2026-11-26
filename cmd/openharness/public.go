package main

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"openharness/spec"
)

// unsafePreviewTools are removed from previews in public mode: they would run
// on the builder's server on behalf of an anonymous visitor.
var unsafePreviewTools = map[string]bool{"shell": true}

// sandboxPreview rewrites a visitor's spec so a public preview can only touch
// its own temp directory and the visitor's own model key. It returns a
// human-readable note for every change. Downloaded binaries are not affected.
func sandboxPreview(s *spec.Spec, dir, apiKey string) []string {
	var notes []string
	var kept []string
	for _, t := range s.Tools.Builtins {
		if unsafePreviewTools[t] {
			notes = append(notes, "Removed the "+t+" tool (it would run on the shared server). It still works in your downloaded binary.")
			continue
		}
		kept = append(kept, t)
	}
	s.Tools.Builtins = kept

	var mcp []spec.MCPServer
	for _, m := range s.Tools.MCPServers {
		if m.Transport == "stdio" {
			notes = append(notes, "Skipped stdio MCP server \""+m.Name+"\" (it would start a process on the shared server).")
			continue
		}
		mcp = append(mcp, m)
	}
	s.Tools.MCPServers = mcp

	s.Permissions.WorkspaceRoot = filepath.Join(dir, "workspace")
	s.Memory.Path = filepath.Join(dir, "memory.json")
	s.Sessions.Dir = filepath.Join(dir, "sessions")
	if s.Observability.TraceFile != "" {
		s.Observability.TraceFile = filepath.Join(dir, "trace.jsonl")
	}
	s.Server.AuthMode = "none"

	usesEnv := false
	refs := []*spec.ModelRef{&s.Model.ModelRef}
	for i := range s.Model.Fallbacks {
		refs = append(refs, &s.Model.Fallbacks[i])
	}
	for _, r := range refs {
		if r.APIKeyEnv != "" {
			usesEnv = true
		}
		r.APIKeyEnv = ""
		if r.APIKey == "" {
			r.APIKey = apiKey
		}
	}
	if usesEnv && apiKey == "" {
		notes = append(notes, "No API key was provided for this test, so model calls will be sent without one. Server environment variables are never used in public previews.")
	}
	return notes
}

// ---------------------------------------------------------------- rate limiting

type bucket struct {
	tokens float64
	last   time.Time
}

// limiter is a per-key token bucket.
type limiter struct {
	mu       sync.Mutex
	capacity float64
	every    time.Duration // one token refills per interval
	buckets  map[string]*bucket
}

func newLimiter(capacity int, every time.Duration) *limiter {
	l := &limiter{capacity: float64(capacity), every: every, buckets: map[string]*bucket{}}
	go func() {
		for range time.Tick(10 * time.Minute) {
			l.mu.Lock()
			for k, b := range l.buckets {
				if time.Since(b.last) > time.Duration(l.capacity)*l.every {
					delete(l.buckets, k)
				}
			}
			l.mu.Unlock()
		}
	}()
	return l
}

// allow takes a token; when empty it reports how long until the next one.
func (l *limiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.capacity, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.capacity, b.tokens+now.Sub(b.last).Seconds()/l.every.Seconds())
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) * float64(l.every))
}

// clientIP uses the last X-Forwarded-For hop (the one our own proxy added)
// only when the builder runs behind a trusted reverse proxy.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
		if ip := r.Header.Get("X-Real-Ip"); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func envBool(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
