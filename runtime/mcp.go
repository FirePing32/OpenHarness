package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"openharness/spec"
)

// mcpClient speaks the Model Context Protocol (JSON-RPC 2.0) to one server.
type mcpClient struct {
	name string
	call func(ctx context.Context, method string, params any) (json.RawMessage, error)
	note func(method string) error
	stop func()
}

type rpcResponse struct {
	ID     *json.RawMessage `json:"id"`
	Method string           `json:"method"`
	Result json.RawMessage  `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

const mcpProtocolVersion = "2025-06-18"

func connectMCP(ctx context.Context, cfg spec.MCPServer, log *Logger) (*mcpClient, error) {
	var c *mcpClient
	var err error
	if cfg.Transport == "http" {
		c = newHTTPMCP(cfg)
	} else {
		c, err = newStdioMCP(cfg, log)
		if err != nil {
			return nil, err
		}
	}
	ictx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err = c.call(ictx, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "openharness", "version": "1.0"},
	})
	if err != nil {
		c.stop()
		return nil, fmt.Errorf("mcp %s initialize: %w", cfg.Name, err)
	}
	_ = c.note("notifications/initialized")
	return c, nil
}

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

func (c *mcpClient) listTools(ctx context.Context) ([]mcpTool, error) {
	var all []mcpTool
	cursor := ""
	for page := 0; page < 20; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var res struct {
			Tools      []mcpTool `json:"tools"`
			NextCursor string    `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, err
		}
		all = append(all, res.Tools...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	return all, nil
}

func (c *mcpClient) callTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(args)) == 0 {
		args = json.RawMessage("{}")
	}
	raw, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return "", err
	}
	var res struct {
		Content []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			MimeType string          `json:"mimeType"`
			Resource json.RawMessage `json:"resource"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return string(raw), nil
	}
	var parts []string
	for _, ct := range res.Content {
		switch ct.Type {
		case "text":
			parts = append(parts, ct.Text)
		case "resource":
			parts = append(parts, string(ct.Resource))
		default:
			parts = append(parts, fmt.Sprintf("[%s content: %s]", ct.Type, ct.MimeType))
		}
	}
	if len(parts) == 0 && len(res.StructuredContent) > 0 {
		parts = append(parts, string(res.StructuredContent))
	}
	text := strings.Join(parts, "\n")
	if res.IsError {
		return "", fmt.Errorf("%s", text)
	}
	return text, nil
}

// ---------------------------------------------------------------- stdio

func newStdioMCP(cfg spec.MCPServer, log *Logger) (*mcpClient, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+expandEnv(v))
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp %s: start %q: %w", cfg.Name, cfg.Command, err)
	}
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			log.Debug("mcp stderr", "server", cfg.Name, "line", sc.Text())
		}
	}()

	var (
		mu      sync.Mutex
		wmu     sync.Mutex
		nextID  atomic.Int64
		pending = map[int64]chan rpcResponse{}
		dead    = make(chan struct{})
	)
	write := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		wmu.Lock()
		defer wmu.Unlock()
		_, err = stdin.Write(append(b, '\n'))
		return err
	}
	go func() {
		defer close(dead)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 32*1024*1024)
		for sc.Scan() {
			var r rpcResponse
			if json.Unmarshal(sc.Bytes(), &r) != nil {
				continue
			}
			if r.ID == nil {
				continue // notification from server
			}
			if r.Method != "" {
				// Server-initiated request (e.g. ping, sampling): answer minimally.
				if r.Method == "ping" {
					_ = write(map[string]any{"jsonrpc": "2.0", "id": r.ID, "result": map[string]any{}})
				} else {
					_ = write(map[string]any{"jsonrpc": "2.0", "id": r.ID, "error": map[string]any{"code": -32601, "message": "not supported"}})
				}
				continue
			}
			var id int64
			if json.Unmarshal(*r.ID, &id) != nil {
				continue
			}
			mu.Lock()
			ch := pending[id]
			delete(pending, id)
			mu.Unlock()
			if ch != nil {
				ch <- r
			}
		}
	}()

	return &mcpClient{
		name: cfg.Name,
		call: func(ctx context.Context, method string, params any) (json.RawMessage, error) {
			id := nextID.Add(1)
			ch := make(chan rpcResponse, 1)
			mu.Lock()
			pending[id] = ch
			mu.Unlock()
			if err := write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
				return nil, err
			}
			select {
			case r := <-ch:
				if r.Error != nil {
					return nil, fmt.Errorf("mcp error %d: %s", r.Error.Code, r.Error.Message)
				}
				return r.Result, nil
			case <-dead:
				return nil, fmt.Errorf("mcp server %s exited", cfg.Name)
			case <-ctx.Done():
				mu.Lock()
				delete(pending, id)
				mu.Unlock()
				return nil, ctx.Err()
			}
		},
		note: func(method string) error {
			return write(map[string]any{"jsonrpc": "2.0", "method": method})
		},
		stop: func() {
			_ = stdin.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
		},
	}, nil
}

// ---------------------------------------------------------------- streamable HTTP

func newHTTPMCP(cfg spec.MCPServer) *mcpClient {
	client := newHTTPClient(120 * time.Second)
	var sessionID atomic.Value
	sessionID.Store("")
	var nextID atomic.Int64

	send := func(ctx context.Context, payload map[string]any) (*http.Response, error) {
		b, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, "POST", cfg.URL, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
		if sid := sessionID.Load().(string); sid != "" {
			req.Header.Set("Mcp-Session-Id", sid)
		}
		for k, v := range cfg.Headers {
			req.Header.Set(k, expandEnv(v))
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
			sessionID.Store(sid)
		}
		if resp.StatusCode >= 400 {
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			return nil, fmt.Errorf("mcp %s: HTTP %d: %s", cfg.Name, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return resp, nil
	}

	return &mcpClient{
		name: cfg.Name,
		call: func(ctx context.Context, method string, params any) (json.RawMessage, error) {
			id := nextID.Add(1)
			resp, err := send(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			var found *rpcResponse
			match := func(data []byte) bool {
				var r rpcResponse
				if json.Unmarshal(data, &r) != nil || r.ID == nil || r.Method != "" {
					return false
				}
				var rid int64
				if json.Unmarshal(*r.ID, &rid) != nil || rid != id {
					return false
				}
				found = &r
				return true
			}
			if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
				_ = sseEvents(resp.Body, func(_, data string) error {
					if match([]byte(data)) {
						return errStop
					}
					return nil
				})
			} else {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, err
				}
				match(body)
			}
			if found == nil {
				return nil, fmt.Errorf("mcp %s: no response for %s", cfg.Name, method)
			}
			if found.Error != nil {
				return nil, fmt.Errorf("mcp error %d: %s", found.Error.Code, found.Error.Message)
			}
			return found.Result, nil
		},
		note: func(method string) error {
			resp, err := send(context.Background(), map[string]any{"jsonrpc": "2.0", "method": method})
			if err == nil {
				resp.Body.Close()
			}
			return err
		},
		stop: func() {},
	}
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func mcpToolName(server, tool string) string {
	n := "mcp_" + server + "_" + unsafeName.ReplaceAllString(tool, "_")
	if len(n) > 64 {
		n = n[:64]
	}
	return n
}
