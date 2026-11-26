// Package builder compiles a HarnessSpec into a standalone runtime binary by
// cross-compiling the embedded runtime source with the spec baked in.
package builder

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"openharness"
	"openharness/spec"
)

// Target is a GOOS/GOARCH pair offered in the UI, grouped by OS family.
type Target struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Family    string `json:"family"`    // macos | linux | windows
	ArchLabel string `json:"archLabel"` // short CPU description
	Label     string `json:"label"`
}

var Targets = []Target{
	{"darwin", "arm64", "macos", "Apple Silicon (M1 and later)", "macOS (Apple Silicon)"},
	{"darwin", "amd64", "macos", "Intel", "macOS (Intel)"},
	{"linux", "amd64", "linux", "x86-64 (most PCs and servers)", "Ubuntu / Linux (x86-64)"},
	{"linux", "arm64", "linux", "ARM64 (Graviton, Ampere, Raspberry Pi)", "Ubuntu / Linux (ARM64)"},
	{"windows", "amd64", "windows", "x64 (most PCs)", "Windows (x64)"},
	{"windows", "arm64", "windows", "ARM64 (Snapdragon)", "Windows (ARM64)"},
}

func HostTarget() Target { return Target{OS: runtime.GOOS, Arch: runtime.GOARCH} }

func findTarget(goos, goarch string) (Target, bool) {
	for _, t := range Targets {
		if t.OS == goos && t.Arch == goarch {
			return t, true
		}
	}
	return Target{}, false
}

func validTarget(goos, goarch string) bool {
	_, ok := findTarget(goos, goarch)
	return ok
}

// GoToolchain locates the go command.
func GoToolchain() (string, error) {
	if p, err := exec.LookPath("go"); err == nil {
		return p, nil
	}
	for _, c := range []string{filepath.Join(runtime.GOROOT(), "bin", "go"), "/usr/local/go/bin/go", "/opt/homebrew/bin/go"} {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", errors.New("Go toolchain not found: install Go 1.26+ (https://go.dev/dl) to build agent binaries")
}

var (
	srcOnce sync.Once
	srcDir  string
	srcErr  error
	sem     = make(chan struct{}, 2)
)

// sourceDir extracts the embedded runtime source once into a stable cache
// directory keyed by content hash, so Go's build cache stays warm.
func sourceDir() (string, error) {
	srcOnce.Do(func() {
		h := sha256.New()
		files := map[string][]byte{}
		srcErr = fs.WalkDir(openharness.Source, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := openharness.Source.ReadFile(p)
			if err != nil {
				return err
			}
			files[p] = b
			h.Write([]byte(p))
			h.Write(b)
			return nil
		})
		if srcErr != nil {
			return
		}
		base, err := os.UserCacheDir()
		if err != nil {
			base = os.TempDir()
		}
		srcDir = filepath.Join(base, "openharness", "src-"+hex.EncodeToString(h.Sum(nil))[:16])
		for p, b := range files {
			dst := filepath.Join(srcDir, filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				srcErr = err
				return
			}
			if err := os.WriteFile(dst, b, 0o644); err != nil {
				srcErr = err
				return
			}
		}
	})
	return srcDir, srcErr
}

// EncodeSpec produces the gzip+base64 payload the runtime decodes at startup.
func EncodeSpec(s *spec.Spec) (string, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.Write(data)
	zw.Close()
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// Filename is the download name for a spec and target.
func Filename(s *spec.Spec, goos, goarch string) string {
	name := fmt.Sprintf("%s-%s-%s", s.Name, goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// Build compiles the binary and returns the path of a temp file the caller must remove.
func Build(ctx context.Context, s *spec.Spec, goos, goarch string) (string, error) {
	if errs := s.Validate(); len(errs) > 0 {
		return "", fmt.Errorf("spec is invalid: %s", strings.Join(errs, "; "))
	}
	if !validTarget(goos, goarch) {
		return "", fmt.Errorf("unsupported target %s/%s", goos, goarch)
	}
	goBin, err := GoToolchain()
	if err != nil {
		return "", err
	}
	dir, err := sourceDir()
	if err != nil {
		return "", fmt.Errorf("extract runtime source: %w", err)
	}
	payload, err := EncodeSpec(s)
	if err != nil {
		return "", err
	}
	out, err := os.CreateTemp("", "openharness-bin-*")
	if err != nil {
		return "", err
	}
	out.Close()

	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		os.Remove(out.Name())
		return "", ctx.Err()
	}
	ldflags := fmt.Sprintf("-s -w -X main.embeddedSpec=%s -X main.version=%s", payload, spec.SchemaVersion)
	cmd := exec.CommandContext(ctx, goBin, "build", "-trimpath", "-ldflags", ldflags, "-o", out.Name(), "./cmd/harness")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0", "GOFLAGS=-mod=mod", "GOWORK=off")
	if msg, err := cmd.CombinedOutput(); err != nil {
		os.Remove(out.Name())
		return "", fmt.Errorf("go build failed: %v\n%s", err, msg)
	}
	return out.Name(), nil
}
