package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"openharness/spec"
)

// Tool is one entry of the tool registry.
type Tool struct {
	Def    ToolDef
	Source string // builtin | http | mcp | skill | memory | subagent
	Group  string // permission group, e.g. "mcp:github"
	Run    func(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry is the ordered tool registry (ctx.tools).
type Registry struct {
	mu    sync.RWMutex
	tools map[string]*Tool
	order []string
}

func NewRegistry() *Registry { return &Registry{tools: map[string]*Tool{}} }

func (r *Registry) Add(t *Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := t.Def.Function.Name
	if _, ok := r.tools[name]; !ok {
		r.order = append(r.order, name)
	}
	r.tools[name] = t
}

func (r *Registry) Get(name string) *Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// Defs returns definitions; when only is non-nil, just those names.
func (r *Registry) Defs(only map[string]bool) []ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []ToolDef
	for _, n := range r.order {
		if only == nil || only[n] {
			out = append(out, r.tools[n].Def)
		}
	}
	return out
}

// turnState is per-request state reachable from tools via the context.
type turnState struct {
	TraceID   string
	SessionID string
	UserKey   string
	Emit      Emitter
	Depth     int

	mu    sync.Mutex
	todos []todoItem

	usage   *Usage // shared with sub-agents so the turn reports total usage
	usageMu *sync.Mutex
}

type todoItem struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type turnKey struct{}

func withTurn(ctx context.Context, ts *turnState) context.Context {
	return context.WithValue(ctx, turnKey{}, ts)
}

func turnFrom(ctx context.Context) *turnState {
	if ts, ok := ctx.Value(turnKey{}).(*turnState); ok {
		return ts
	}
	return &turnState{UserKey: "default", Emit: nopEmitter{}}
}

func decodeArgs(raw json.RawMessage, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

// truncate shortens tool output per the configured strategy.
func truncate(s string, max int, mode string) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	omitted := len(s) - max
	if mode == "head" {
		return s[:max] + fmt.Sprintf("\n…[%d chars truncated]", omitted)
	}
	head := max * 2 / 3
	tail := max - head
	return s[:head] + fmt.Sprintf("\n…[%d chars truncated]…\n", omitted) + s[len(s)-tail:]
}

// ---------------------------------------------------------------- built-ins

type workspace struct{ root string }

func (w workspace) resolve(p string) (string, error) {
	root, err := filepath.Abs(w.root)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	if realRoot, err := filepath.EvalSymlinks(root); err == nil {
		root = realRoot
	}
	full := filepath.Clean(filepath.Join(root, p))
	if real, err := filepath.EvalSymlinks(full); err == nil {
		full = real
	}
	if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q is outside the workspace", p)
	}
	return full, nil
}

func builtinTools(s *spec.Spec) map[string]*Tool {
	ws := workspace{s.Permissions.WorkspaceRoot}
	tools := map[string]*Tool{}
	add := func(name, desc, params string, run func(context.Context, json.RawMessage) (string, error)) {
		tools[name] = &Tool{Def: makeToolDef(name, desc, params), Source: "builtin", Run: run}
	}

	add("current_time", "Get the current date and time. Optionally pass an IANA time zone such as Europe/Paris.",
		`{"type":"object","properties":{"timezone":{"type":"string","description":"IANA time zone, default UTC"}}}`,
		func(_ context.Context, raw json.RawMessage) (string, error) {
			var a struct{ Timezone string }
			if err := decodeArgs(raw, &a); err != nil {
				return "", err
			}
			loc := time.UTC
			if a.Timezone != "" {
				l, err := time.LoadLocation(a.Timezone)
				if err != nil {
					return "", fmt.Errorf("unknown time zone %q", a.Timezone)
				}
				loc = l
			}
			now := time.Now().In(loc)
			return now.Format("Monday, 2 January 2006 15:04:05 MST (Z07:00)"), nil
		})

	add("calculator", "Evaluate an arithmetic expression exactly. Supports + - * / % ^, parentheses, pi, e, sqrt, abs, round, floor, ceil, ln, log, log2, exp, sin, cos, tan.",
		`{"type":"object","properties":{"expression":{"type":"string"}},"required":["expression"]}`,
		func(_ context.Context, raw json.RawMessage) (string, error) {
			var a struct{ Expression string }
			if err := decodeArgs(raw, &a); err != nil {
				return "", err
			}
			v, err := evalExpr(a.Expression)
			if err != nil {
				return "", err
			}
			if v == math.Trunc(v) && math.Abs(v) < 1e15 {
				return strconv.FormatInt(int64(v), 10), nil
			}
			return strconv.FormatFloat(v, 'g', 15, 64), nil
		})

	fetchClient := newHTTPClient(30 * time.Second)
	add("http_fetch", "Fetch a web page or API over HTTP(S) with GET and return its text (HTML is converted to plain text).",
		`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`,
		func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct{ URL string }
			if err := decodeArgs(raw, &a); err != nil {
				return "", err
			}
			u, err := url.Parse(a.URL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
				return "", fmt.Errorf("only http(s) URLs are allowed")
			}
			if !domainAllowed(u.Hostname(), s.Tools.Fetch.AllowedDomains) {
				return "", fmt.Errorf("domain %q is not in the allowlist", u.Hostname())
			}
			req, _ := http.NewRequestWithContext(ctx, "GET", a.URL, nil)
			req.Header.Set("User-Agent", "OpenHarness/1.0 (+agent)")
			resp, err := fetchClient.Do(req)
			if err != nil {
				return "", err
			}
			defer resp.Body.Close()
			max := s.Tools.Fetch.MaxBytes
			if max <= 0 {
				max = 200000
			}
			body, err := io.ReadAll(io.LimitReader(resp.Body, int64(max)))
			if err != nil {
				return "", err
			}
			text := string(body)
			if strings.Contains(resp.Header.Get("Content-Type"), "html") {
				text = htmlToText(text)
			}
			return fmt.Sprintf("HTTP %d %s\n\n%s", resp.StatusCode, a.URL, text), nil
		})

	add("read_file", "Read a UTF-8 text file from the workspace. Paths are relative to the workspace root.",
		`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","description":"first line (1-based)"},"limit":{"type":"integer","description":"max lines"}},"required":["path"]}`,
		func(_ context.Context, raw json.RawMessage) (string, error) {
			var a struct {
				Path          string
				Offset, Limit int
			}
			if err := decodeArgs(raw, &a); err != nil {
				return "", err
			}
			p, err := ws.resolve(a.Path)
			if err != nil {
				return "", err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return "", err
			}
			if a.Offset <= 0 && a.Limit <= 0 {
				return string(b), nil
			}
			lines := strings.Split(string(b), "\n")
			start := max(a.Offset-1, 0)
			end := len(lines)
			if a.Limit > 0 && start+a.Limit < end {
				end = start + a.Limit
			}
			if start >= len(lines) {
				return "", nil
			}
			return strings.Join(lines[start:end], "\n"), nil
		})

	add("list_dir", "List files and directories in the workspace.",
		`{"type":"object","properties":{"path":{"type":"string","description":"relative directory, default ."}}}`,
		func(_ context.Context, raw json.RawMessage) (string, error) {
			var a struct{ Path string }
			if err := decodeArgs(raw, &a); err != nil {
				return "", err
			}
			if a.Path == "" {
				a.Path = "."
			}
			p, err := ws.resolve(a.Path)
			if err != nil {
				return "", err
			}
			entries, err := os.ReadDir(p)
			if err != nil {
				return "", err
			}
			var sb strings.Builder
			for _, e := range entries {
				if e.IsDir() {
					sb.WriteString(e.Name() + "/\n")
				} else if info, err := e.Info(); err == nil {
					fmt.Fprintf(&sb, "%s (%d bytes)\n", e.Name(), info.Size())
				}
			}
			if sb.Len() == 0 {
				return "(empty directory)", nil
			}
			return sb.String(), nil
		})

	add("write_file", "Create or overwrite a text file in the workspace.",
		`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`,
		func(_ context.Context, raw json.RawMessage) (string, error) {
			if s.Tools.Files.ReadOnly {
				return "", errors.New("the workspace is read-only")
			}
			var a struct{ Path, Content string }
			if err := decodeArgs(raw, &a); err != nil {
				return "", err
			}
			p, err := ws.resolve(a.Path)
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(p, []byte(a.Content), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), a.Path), nil
		})

	add("shell", "Run one program in the workspace directory (no shell interpretation: pass the program and its arguments separately).",
		`{"type":"object","properties":{"command":{"type":"string","description":"program name, e.g. ls"},"args":{"type":"array","items":{"type":"string"}}},"required":["command"]}`,
		func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct {
				Command string
				Args    []string
			}
			if err := decodeArgs(raw, &a); err != nil {
				return "", err
			}
			if strings.ContainsAny(a.Command, "/\\") || a.Command == "" {
				return "", errors.New("command must be a bare program name")
			}
			if len(s.Tools.Shell.AllowedCommands) > 0 && !contains(s.Tools.Shell.AllowedCommands, a.Command) {
				return "", fmt.Errorf("command %q is not in the allowlist (%s)", a.Command, strings.Join(s.Tools.Shell.AllowedCommands, ", "))
			}
			dir, err := ws.resolve(".")
			if err != nil {
				return "", err
			}
			timeout := time.Duration(s.Tools.Shell.TimeoutSec) * time.Second
			if timeout <= 0 {
				timeout = 30 * time.Second
			}
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			cmd := exec.CommandContext(cctx, a.Command, a.Args...)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			res := string(out)
			if err != nil {
				return fmt.Sprintf("%s\n[exit: %v]", res, err), nil
			}
			if res == "" {
				res = "(no output, exit 0)"
			}
			return res, nil
		})

	add("todo", "Maintain a checklist for the current task. action=set replaces the list, action=done marks items complete, action=list shows it.",
		`{"type":"object","properties":{"action":{"type":"string","enum":["set","done","list"]},"items":{"type":"array","items":{"type":"string"}},"indexes":{"type":"array","items":{"type":"integer"},"description":"1-based item numbers for done"}},"required":["action"]}`,
		func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct {
				Action  string
				Items   []string
				Indexes []int
			}
			if err := decodeArgs(raw, &a); err != nil {
				return "", err
			}
			ts := turnFrom(ctx)
			ts.mu.Lock()
			defer ts.mu.Unlock()
			switch a.Action {
			case "set":
				ts.todos = nil
				for _, it := range a.Items {
					ts.todos = append(ts.todos, todoItem{Text: it})
				}
			case "done":
				for _, i := range a.Indexes {
					if i >= 1 && i <= len(ts.todos) {
						ts.todos[i-1].Done = true
					}
				}
			case "list":
			default:
				return "", fmt.Errorf("unknown action %q", a.Action)
			}
			var sb strings.Builder
			for i, t := range ts.todos {
				mark := " "
				if t.Done {
					mark = "x"
				}
				fmt.Fprintf(&sb, "%d. [%s] %s\n", i+1, mark, t.Text)
			}
			if sb.Len() == 0 {
				return "(no items)", nil
			}
			return sb.String(), nil
		})
	return tools
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func domainAllowed(host string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	host = strings.ToLower(host)
	for _, d := range allowed {
		d = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d), "*."))
		if d != "" && (host == d || strings.HasSuffix(host, "."+d)) {
			return true
		}
	}
	return false
}

var (
	reScript = regexp.MustCompile(`(?is)<(script|style|noscript|svg|head)[^>]*>.*?</(script|style|noscript|svg|head)>`)
	reBlock  = regexp.MustCompile(`(?i)</?(p|div|br|li|tr|h[1-6]|section|article|header|footer|ul|ol|table|pre)[^>]*>`)
	reTag    = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpaces = regexp.MustCompile(`[ \t\r\f\v]+`)
	reLines  = regexp.MustCompile(`\n\s*\n+`)
)

func htmlToText(s string) string {
	s = reScript.ReplaceAllString(s, " ")
	s = reBlock.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = reSpaces.ReplaceAllString(s, " ")
	s = reLines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// ---------------------------------------------------------------- HTTP tools

var placeholderRe = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

func httpTool(t spec.HTTPTool, timeout time.Duration) *Tool {
	params := string(t.Parameters)
	if strings.TrimSpace(params) == "" || params == "null" {
		params = `{"type":"object","properties":{}}`
	}
	client := newHTTPClient(timeout)
	return &Tool{
		Def:    makeToolDef(t.Name, t.Description, params),
		Source: "http",
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			args := map[string]any{}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			used := map[string]bool{}
			u := placeholderRe.ReplaceAllStringFunc(t.URL, func(m string) string {
				key := placeholderRe.FindStringSubmatch(m)[1]
				used[key] = true
				return url.QueryEscape(fmt.Sprint(args[key]))
			})
			method := strings.ToUpper(t.Method)
			if method == "" {
				method = "POST"
			}
			var body io.Reader
			if method == "GET" {
				pu, err := url.Parse(u)
				if err != nil {
					return "", err
				}
				q := pu.Query()
				for k, v := range args {
					if !used[k] {
						q.Set(k, fmt.Sprint(v))
					}
				}
				pu.RawQuery = q.Encode()
				u = pu.String()
			} else {
				b, _ := json.Marshal(args)
				body = bytes.NewReader(b)
			}
			req, err := http.NewRequestWithContext(ctx, method, u, body)
			if err != nil {
				return "", err
			}
			if body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			for k, v := range t.Headers {
				req.Header.Set(k, expandEnv(v))
			}
			resp, err := client.Do(req)
			if err != nil {
				return "", err
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
			if resp.StatusCode >= 400 {
				return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(b), 2000, "head"))
			}
			return string(b), nil
		},
	}
}
