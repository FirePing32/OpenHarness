package runtime

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"openharness/spec"
)

// Logger wraps slog and an optional JSONL trace sink for replayable events.
type Logger struct {
	*slog.Logger
	mu    sync.Mutex
	trace *os.File
}

func NewLogger(o spec.ObservabilitySpec) *Logger {
	var lvl slog.Level
	switch o.LogLevel {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler = slog.NewTextHandler(os.Stderr, opts)
	if o.LogFormat == "json" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	}
	l := &Logger{Logger: slog.New(h)}
	if o.TraceFile != "" {
		_ = os.MkdirAll(filepath.Dir(o.TraceFile), 0o755)
		f, err := os.OpenFile(o.TraceFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			l.Warn("cannot open trace file", "path", o.TraceFile, "err", err.Error())
		} else {
			l.trace = f
		}
	}
	return l
}

// Event records one harness event (the session/event log in DeepSeek terms).
func (l *Logger) Event(traceID, typ string, data map[string]any) {
	l.Debug(typ, "trace", traceID, "data", data)
	if l.trace == nil {
		return
	}
	b, err := json.Marshal(map[string]any{"ts": time.Now().UTC().Format(time.RFC3339Nano), "traceId": traceID, "type": typ, "data": data})
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.trace.Write(append(b, '\n'))
}

func (l *Logger) Close() {
	if l.trace != nil {
		_ = l.trace.Close()
	}
}
