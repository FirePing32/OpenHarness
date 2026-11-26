// Command harness is the agent runtime. The builder compiles one copy per
// agent with its HarnessSpec embedded via -ldflags, producing a standalone
// binary that serves an OpenAI-compatible API.
package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"openharness/runtime"
	"openharness/spec"
)

// embeddedSpec is gzip+base64 JSON injected at build time.
var embeddedSpec string

var version = "dev"

func loadEmbedded() ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(embeddedSpec)
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}

func main() {
	specPath := flag.String("spec", "", "path to a harness spec JSON (overrides the embedded spec)")
	host := flag.String("host", "", "listen host (overrides spec)")
	port := flag.Int("port", 0, "listen port (overrides spec; env PORT also works)")
	printSpec := flag.Bool("print-spec", false, "print the effective spec as JSON and exit")
	check := flag.Bool("check", false, "validate the spec and exit")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("openharness runtime", version)
		return
	}

	var data []byte
	var err error
	switch {
	case *specPath != "":
		data, err = os.ReadFile(*specPath)
	case embeddedSpec != "":
		data, err = loadEmbedded()
	default:
		err = errors.New("no spec embedded in this binary; pass --spec harness.json")
	}
	if err != nil {
		fatal(err)
	}
	s, err := spec.Parse(data)
	if err != nil {
		fatal(err)
	}
	if *host != "" {
		s.Server.Host = *host
	}
	if *port > 0 {
		s.Server.Port = *port
	} else if p, err := strconv.Atoi(os.Getenv("PORT")); err == nil && p > 0 {
		s.Server.Port = p
	}
	if *printSpec {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(s)
		return
	}
	if errs := s.Validate(); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "spec error:", e)
		}
		os.Exit(1)
	}
	if *check {
		fmt.Println("spec OK:", s.Name)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv, err := runtime.NewServer(ctx, s)
	if err != nil {
		fatal(err)
	}
	defer srv.Close()

	addr := net.JoinHostPort(s.Server.Host, strconv.Itoa(s.Server.Port))
	httpSrv := &http.Server{Addr: addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(sctx)
	}()
	srv.Logger().Info("harness listening", "agent", s.Name, "url", "http://"+addr+"/v1", "model", s.Server.ModelID)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
