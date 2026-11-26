// Command openharness is the builder: a local web app where users customize
// every harness component, test the agent live, and download a binary.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"openharness/builder"
	"openharness/runtime"
	"openharness/spec"
	"openharness/web"
)

type preview struct {
	id       string
	owner    string
	dir      string // temp dir in public mode, removed on close
	srv      *runtime.Server
	handler  http.Handler
	created  time.Time
	lastUsed time.Time
}

type config struct {
	public          bool
	trustProxy      bool
	maxPreviews     int
	previewsPerIP   int
	previewIdle     time.Duration
	buildPerHour    int
	previewPerHour  int
	messagesPerHour int
}

type app struct {
	cfg      config
	mu       sync.Mutex
	previews []*preview
	log      *slog.Logger

	buildLimit, previewLimit, chatLimit *limiter
}

func main() {
	defAddr := "127.0.0.1:7070"
	if v := os.Getenv("OPENHARNESS_ADDR"); v != "" {
		defAddr = v
	} else if p := os.Getenv("PORT"); p != "" {
		defAddr = "0.0.0.0:" + p
	}
	addr := flag.String("addr", defAddr, "listen address (env OPENHARNESS_ADDR or PORT)")
	public := flag.Bool("public", envBool("OPENHARNESS_PUBLIC"), "public mode: sandbox previews, rate-limit, never expose server env (env OPENHARNESS_PUBLIC)")
	trustProxy := flag.Bool("trust-proxy", envBool("OPENHARNESS_TRUST_PROXY"), "read the client IP from X-Forwarded-For (only behind your own proxy; env OPENHARNESS_TRUST_PROXY)")
	maxPreviews := flag.Int("max-previews", 0, "max concurrent test instances (default 4 local, 40 public)")
	warm := flag.Bool("warm", false, "compile the runtime for every target once to fill the Go build cache, then exit")
	flag.Parse()

	if *warm {
		for _, t := range builder.Targets {
			start := time.Now()
			path, err := builder.Build(context.Background(), spec.Default(), t.OS, t.Arch)
			if err != nil {
				fmt.Fprintln(os.Stderr, "warm:", t.OS+"/"+t.Arch, err)
				os.Exit(1)
			}
			os.Remove(path)
			fmt.Printf("warmed %s/%s in %s\n", t.OS, t.Arch, time.Since(start).Round(time.Millisecond))
		}
		return
	}

	cfg := config{
		public: *public, trustProxy: *trustProxy, maxPreviews: *maxPreviews,
		previewsPerIP: 2, previewIdle: 30 * time.Minute,
		buildPerHour: 20, previewPerHour: 30, messagesPerHour: 120,
	}
	if cfg.maxPreviews <= 0 {
		cfg.maxPreviews = 4
		if cfg.public {
			cfg.maxPreviews = 40
		}
	}
	a := &app{cfg: cfg, log: slog.New(slog.NewTextHandler(os.Stderr, nil))}
	if cfg.public {
		// A visitor's spec must never read this server's environment or reach
		// its private network (Coolify's internal services, cloud metadata...).
		runtime.LookupEnv = func(string) string { return "" }
		runtime.BlockPrivateNetworks = true
		a.buildLimit = newLimiter(cfg.buildPerHour, time.Hour/time.Duration(cfg.buildPerHour))
		a.previewLimit = newLimiter(cfg.previewPerHour, time.Hour/time.Duration(cfg.previewPerHour))
		a.chatLimit = newLimiter(cfg.messagesPerHour/4, time.Hour/time.Duration(cfg.messagesPerHour))
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServerFS(web.FS))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"status": "ok"}) })
	mux.HandleFunc("GET /api/catalog", a.catalog)
	mux.HandleFunc("POST /api/validate", a.validate)
	mux.HandleFunc("POST /api/preview", a.startPreview)
	mux.HandleFunc("POST /api/build", a.build)
	mux.HandleFunc("/preview/{id}/", a.routePreview)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Addr: *addr, Handler: securityHeaders(mux), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
		a.closeAll()
	}()
	go a.janitor(ctx)
	if _, err := builder.GoToolchain(); err != nil {
		a.log.Warn("binary builds will fail", "err", err.Error())
	}
	mode := "local"
	if cfg.public {
		mode = "public"
	}
	a.log.Info("OpenHarness builder running", "url", "http://"+*addr, "mode", mode)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// limited applies a per-IP token bucket in public mode.
func (a *app) limited(w http.ResponseWriter, r *http.Request, l *limiter, what string) bool {
	if l == nil {
		return false
	}
	ok, wait := l.allow(clientIP(r, a.cfg.trustProxy))
	if ok {
		return false
	}
	secs := int(wait.Seconds()) + 1
	w.Header().Set("Retry-After", fmt.Sprint(secs))
	if strings.HasPrefix(r.URL.Path, "/preview/") {
		writeJSON(w, 429, map[string]any{"error": map[string]any{"message": fmt.Sprintf("Too many %s. Try again in %ds.", what, secs), "type": "rate_limit"}})
	} else {
		errJSON(w, 429, fmt.Sprintf("Too many %s. Try again in %ds.", what, secs))
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errJSON(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func (a *app) catalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"components":   spec.Components,
		"builtinTools": spec.BuiltinTools,
		"hookEvents":   spec.HookEvents,
		"presets":      spec.Presets(),
		"targets":      builder.Targets,
		"hostTarget":   builder.HostTarget(),
		"defaults":     spec.Default(),
		"public":       a.cfg.public,
	})
}

type specBody struct {
	Spec   json.RawMessage `json:"spec"`
	OS     string          `json:"os"`
	Arch   string          `json:"arch"`
	APIKey string          `json:"apiKey"` // public previews only; held in memory
}

func readSpec(r *http.Request) (*spec.Spec, *specBody, error) {
	var b specBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&b); err != nil {
		return nil, nil, fmt.Errorf("invalid request body: %w", err)
	}
	if len(b.Spec) == 0 {
		return nil, nil, errors.New("missing spec")
	}
	s, err := spec.Parse(b.Spec)
	return s, &b, err
}

func (a *app) validate(w http.ResponseWriter, r *http.Request) {
	s, _, err := readSpec(r)
	if err != nil {
		writeJSON(w, 200, map[string]any{"errors": []string{err.Error()}})
		return
	}
	errs := s.Validate()
	if errs == nil {
		errs = []string{}
	}
	writeJSON(w, 200, map[string]any{"errors": errs})
}

func (a *app) startPreview(w http.ResponseWriter, r *http.Request) {
	if a.limited(w, r, a.previewLimit, "test instances") {
		return
	}
	s, body, err := readSpec(r)
	if err != nil {
		errJSON(w, 400, err.Error())
		return
	}
	if errs := s.Validate(); len(errs) > 0 {
		writeJSON(w, 400, map[string]any{"error": "spec is invalid", "errors": errs})
		return
	}
	p := &preview{id: "p" + randomID(), owner: clientIP(r, a.cfg.trustProxy), created: time.Now(), lastUsed: time.Now()}
	var notes []string
	if a.cfg.public {
		dir, err := os.MkdirTemp("", "oh-preview-*")
		if err != nil {
			errJSON(w, 500, err.Error())
			return
		}
		p.dir = dir
		notes = sandboxPreview(s, dir, body.APIKey)
	}
	// Previews always stream agent activity so the playground can show it.
	s.Server.StreamToolEvents = true
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	srv, err := runtime.NewServer(ctx, s)
	if err != nil {
		if p.dir != "" {
			os.RemoveAll(p.dir)
		}
		errJSON(w, 400, err.Error())
		return
	}
	p.srv = srv
	p.handler = http.StripPrefix("/preview/"+p.id, srv.Handler())

	a.mu.Lock()
	var evicted []*preview
	if a.cfg.public {
		// Each visitor keeps at most previewsPerIP instances; their oldest goes first.
		var mine int
		for _, x := range a.previews {
			if x.owner == p.owner {
				mine++
			}
		}
		for i := 0; mine >= a.cfg.previewsPerIP && i < len(a.previews); {
			if a.previews[i].owner == p.owner {
				evicted = append(evicted, a.previews[i])
				a.previews = append(a.previews[:i], a.previews[i+1:]...)
				mine--
				continue
			}
			i++
		}
	}
	a.previews = append(a.previews, p)
	for len(a.previews) > a.cfg.maxPreviews {
		evicted = append(evicted, a.previews[0])
		a.previews = a.previews[1:]
	}
	a.mu.Unlock()
	for _, e := range evicted {
		e.close()
	}
	a.log.Info("preview started", "id", p.id, "agent", s.Name)
	if notes == nil {
		notes = []string{}
	}
	writeJSON(w, 200, map[string]any{"id": p.id, "baseUrl": "/preview/" + p.id + "/v1", "model": s.Server.ModelID, "notes": notes})
}

func (p *preview) close() {
	p.srv.Close()
	if p.dir != "" {
		os.RemoveAll(p.dir)
	}
}

// janitor closes previews that have been idle too long.
func (a *app) janitor(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.mu.Lock()
		var keep, expired []*preview
		for _, p := range a.previews {
			if time.Since(p.lastUsed) > a.cfg.previewIdle {
				expired = append(expired, p)
			} else {
				keep = append(keep, p)
			}
		}
		a.previews = keep
		a.mu.Unlock()
		for _, p := range expired {
			p.close()
		}
	}
}

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (a *app) routePreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.mu.Lock()
	var p *preview
	for _, x := range a.previews {
		if x.id == id {
			p = x
			x.lastUsed = time.Now()
		}
	}
	a.mu.Unlock()
	if p == nil {
		writeJSON(w, 404, map[string]any{"error": map[string]any{"message": "Test instance not found or expired. Start a new one.", "type": "not_found"}})
		return
	}
	if r.Method == http.MethodPost && a.limited(w, r, a.chatLimit, "messages") {
		return
	}
	p.handler.ServeHTTP(w, r)
}

func (a *app) closeAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.previews {
		p.close()
	}
	a.previews = nil
}

func (a *app) build(w http.ResponseWriter, r *http.Request) {
	if a.limited(w, r, a.buildLimit, "builds") {
		return
	}
	s, b, err := readSpec(r)
	if err != nil {
		errJSON(w, 400, err.Error())
		return
	}
	goos, goarch := b.OS, b.Arch
	if goos == "" || goarch == "" {
		h := builder.HostTarget()
		goos, goarch = h.OS, h.Arch
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	start := time.Now()
	path, err := builder.Build(ctx, s, goos, goarch)
	if err != nil {
		code := 500
		if strings.HasPrefix(err.Error(), "spec is invalid") || strings.HasPrefix(err.Error(), "unsupported target") {
			code = 400
		}
		errJSON(w, code, err.Error())
		return
	}
	defer os.Remove(path)
	f, err := os.Open(path)
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	name := builder.Filename(s, goos, goarch)
	a.log.Info("binary built", "agent", s.Name, "target", goos+"/"+goarch, "bytes", st.Size(), "took", time.Since(start).Round(time.Millisecond))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	w.Header().Set("Content-Length", fmt.Sprint(st.Size()))
	w.Header().Set("X-Build-Ms", fmt.Sprint(time.Since(start).Milliseconds()))
	_, _ = io.Copy(w, f)
}
