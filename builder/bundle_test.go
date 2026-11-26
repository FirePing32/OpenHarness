package builder

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"openharness/spec"
)

func TestDockerfileDistroless(t *testing.T) {
	s := spec.Default()
	s.Name = "My-Agent"
	df := Dockerfile(s)
	for _, want := range []string{
		"FROM gcr.io/distroless/static-debian12:nonroot",
		"COPY --chmod=0755 docker/linux-${TARGETARCH}/agent /usr/local/bin/agent",
		`ENTRYPOINT ["/usr/local/bin/agent", "--host", "0.0.0.0"]`,
		"ENV PORT=8080", "EXPOSE 8080", "--healthcheck",
		"docker build -t my-agent .", "-e DEEPSEEK_API_KEY",
	} {
		if !strings.Contains(df, want) {
			t.Errorf("Dockerfile missing %q:\n%s", want, df)
		}
	}
	if strings.Contains(df, "debian:bookworm-slim") {
		t.Error("tool-free agent should use distroless")
	}
	if c := Compose(s); !strings.Contains(c, "- DEEPSEEK_API_KEY") || !strings.Contains(c, "my-agent-data:/data") {
		t.Errorf("compose:\n%s", c)
	}
}

func TestDockerfileWithShell(t *testing.T) {
	s := spec.Presets()[2].Spec // coding agent: shell with git, go, npm, python3, pytest...
	s.Tools.Shell.AllowedCommands = append(s.Tools.Shell.AllowedCommands, "terraform")
	df := Dockerfile(s)
	for _, want := range []string{"FROM debian:bookworm-slim", "git", "golang-go", "python3-pytest", "npm", `# TODO: install "terraform"`, "USER agent"} {
		if !strings.Contains(df, want) {
			t.Errorf("Dockerfile missing %q:\n%s", want, df)
		}
	}
}

func TestBundle(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles three binaries")
	}
	if _, err := GoToolchain(); err != nil {
		t.Skip(err)
	}
	s := spec.Default()
	goos, goarch := runtime.GOOS, runtime.GOARCH
	if _, ok := findTarget(goos, goarch); !ok {
		t.Skip("host is not a supported target")
	}
	p, err := Bundle(context.Background(), s, goos, goarch)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	got := map[string]os.FileMode{}
	dir := t.TempDir()
	for _, f := range zr.File {
		got[f.Name] = f.Mode()
		if f.Name == "my-agent/"+Filename(s, goos, goarch) {
			rc, _ := f.Open()
			b := make([]byte, f.UncompressedSize64)
			_, _ = io.ReadFull(rc, b)
			rc.Close()
			_ = os.WriteFile(filepath.Join(dir, "agent"), b, 0o755)
		}
	}
	for _, name := range []string{"Dockerfile", "docker-compose.yml", ".dockerignore", "README.md", "docker/linux-amd64/agent", "docker/linux-arm64/agent", Filename(s, goos, goarch)} {
		if _, ok := got["my-agent/"+name]; !ok {
			t.Errorf("bundle missing %s (have %v)", name, got)
		}
	}
	if m := got["my-agent/docker/linux-arm64/agent"]; m&0o111 == 0 {
		t.Errorf("linux binary not executable: %v", m)
	}
	// The native binary must answer --healthcheck with failure when nothing listens.
	out, err := exec.Command(filepath.Join(dir, "agent"), "--healthcheck", "--port", "1").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "unhealthy") {
		t.Errorf("healthcheck should fail with no server: err=%v out=%s", err, out)
	}
}
