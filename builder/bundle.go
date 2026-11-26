package builder

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"openharness/spec"
)

// Docker images run Linux even on macOS and Windows hosts, so a bundle ships
// Linux builds for both CPU families next to the native binary.
var dockerArchs = []string{"amd64", "arm64"}

// BundleFilename is the download name of a Docker bundle.
func BundleFilename(s *spec.Spec, goos, goarch string) string {
	return fmt.Sprintf("%s-%s-%s-docker.zip", s.Name, goos, goarch)
}

// Bundle builds a zip holding the native binary for goos/goarch, Linux
// binaries for the container, a Dockerfile, docker-compose.yml and a README.
// It returns the path of a temp file the caller must remove.
func Bundle(ctx context.Context, s *spec.Spec, goos, goarch string) (string, error) {
	t, ok := findTarget(goos, goarch)
	if !ok {
		return "", fmt.Errorf("unsupported target %s/%s", goos, goarch)
	}
	native, err := Build(ctx, s, goos, goarch)
	if err != nil {
		return "", err
	}
	defer os.Remove(native)
	linux := map[string]string{}
	for _, arch := range dockerArchs {
		if goos == "linux" && goarch == arch {
			linux[arch] = native
			continue
		}
		p, err := Build(ctx, s, "linux", arch)
		if err != nil {
			return "", err
		}
		defer os.Remove(p)
		linux[arch] = p
	}

	out, err := os.CreateTemp("", "openharness-bundle-*.zip")
	if err != nil {
		return "", err
	}
	ok = false
	defer func() {
		if !ok {
			out.Close()
			os.Remove(out.Name())
		}
	}()
	zw := zip.NewWriter(out)
	root := s.Name + "/"
	if err := addFile(zw, root+Filename(s, goos, goarch), native, 0o755); err != nil {
		return "", err
	}
	for _, arch := range dockerArchs {
		if err := addFile(zw, root+"docker/linux-"+arch+"/agent", linux[arch], 0o755); err != nil {
			return "", err
		}
	}
	texts := map[string]string{
		"Dockerfile":         Dockerfile(s),
		"docker-compose.yml": Compose(s),
		".dockerignore":      dockerignore(s, goos, goarch),
		"README.md":          BundleReadme(s, t),
	}
	names := make([]string, 0, len(texts))
	for n := range texts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := addText(zw, root+n, texts[n]); err != nil {
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	ok = true
	return out.Name(), nil
}

func addFile(zw *zip.Writer, name, src string, mode os.FileMode) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()}
	h.SetMode(mode) // keeps the executable bit for unzip on macOS/Linux
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

func addText(zw *zip.Writer, name, body string) error {
	h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()}
	h.SetMode(0o644)
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, body)
	return err
}

// imageName is a Docker-safe (lowercase) name for the agent.
func imageName(s *spec.Spec) string { return strings.ToLower(s.Name) }

// envVars lists the environment variables the binary reads at runtime.
func envVars(s *spec.Spec) []string {
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if s.Model.APIKey == "" {
		add(s.Model.APIKeyEnv)
	}
	for _, f := range s.Model.Fallbacks {
		if f.APIKey == "" {
			add(f.APIKeyEnv)
		}
	}
	if s.Server.AuthMode == "bearer" {
		add(s.Server.APIKeyEnv)
	}
	return out
}

// aptPackages maps commands the agent may run to Debian packages. Commands
// already in debian:bookworm-slim (ls, cat, grep, find, sed, ...) need nothing.
var aptPackages = map[string]string{
	"git": "git", "curl": "curl", "wget": "wget", "jq": "jq", "make": "make",
	"python3": "python3", "python": "python3", "pip": "python3-pip", "pip3": "python3-pip",
	"pytest": "python3-pytest", "node": "nodejs", "npm": "npm", "npx": "npm",
	"go": "golang-go", "rg": "ripgrep", "zip": "zip", "unzip": "unzip",
	"tree": "tree", "file": "file", "sqlite3": "sqlite3",
}

var slimBuiltins = map[string]bool{
	"ls": true, "cat": true, "grep": true, "find": true, "sed": true, "awk": true, "head": true, "tail": true,
	"wc": true, "echo": true, "sort": true, "uniq": true, "cut": true, "tr": true, "diff": true, "du": true,
	"df": true, "pwd": true, "date": true, "env": true, "mkdir": true, "touch": true, "cp": true, "mv": true,
	"rm": true, "stat": true, "xargs": true, "tee": true, "basename": true, "dirname": true, "true": true,
	"false": true, "sh": true, "bash": true, "tar": true, "gzip": true,
}

// needsOS reports whether the agent runs local programs, which a distroless
// image cannot provide.
func needsOS(s *spec.Spec) (pkgs []string, unknown []string, need bool) {
	cmds := map[string]bool{}
	for _, b := range s.Tools.Builtins {
		if b == "shell" {
			need = true
			for _, c := range s.Tools.Shell.AllowedCommands {
				cmds[c] = true
			}
		}
	}
	for _, m := range s.Tools.MCPServers {
		if m.Transport == "stdio" {
			need = true
			cmds[m.Command] = true
		}
	}
	seen := map[string]bool{}
	for c := range cmds {
		if p, ok := aptPackages[c]; ok {
			if !seen[p] {
				seen[p] = true
				pkgs = append(pkgs, p)
			}
		} else if !slimBuiltins[c] && c != "" {
			unknown = append(unknown, c)
		}
	}
	sort.Strings(pkgs)
	sort.Strings(unknown)
	return pkgs, unknown, need
}

// Dockerfile renders a multi-arch Dockerfile for the agent. It picks a
// distroless base unless the agent runs local programs (shell tool or stdio
// MCP servers), in which case it uses Debian with the needed packages.
func Dockerfile(s *spec.Spec) string {
	var b strings.Builder
	img := imageName(s)
	port := s.Server.Port
	envs := envVars(s)
	runEnv := ""
	for _, e := range envs {
		runEnv += " -e " + e
	}
	fmt.Fprintf(&b, "# %s: an OpenHarness agent serving an OpenAI-compatible API on port %d.\n", s.Name, port)
	b.WriteString("#\n")
	fmt.Fprintf(&b, "#   docker build -t %s .\n", img)
	fmt.Fprintf(&b, "#   docker run --rm -p %d:%d%s -v %s-data:/data %s\n", port, port, runEnv, img, img)
	b.WriteString("#\n")
	b.WriteString("# Multi-arch: Docker picks docker/linux-amd64 or docker/linux-arm64 for the\n")
	b.WriteString("# machine it builds for (requires BuildKit, the default since Docker 23).\n")
	if len(envs) > 0 {
		b.WriteString("# Secrets are read from the environment at run time; none are baked in here.\n")
	}
	b.WriteString("\n")

	pkgs, unknown, full := needsOS(s)
	if full {
		b.WriteString("# The agent runs local programs (shell tool or stdio MCP servers), so it\n")
		b.WriteString("# needs a full OS image rather than distroless.\n")
		b.WriteString("FROM debian:bookworm-slim\n")
		install := append([]string{"ca-certificates", "tzdata"}, pkgs...)
		fmt.Fprintf(&b, "RUN apt-get update \\\n && apt-get install -y --no-install-recommends %s \\\n && rm -rf /var/lib/apt/lists/* \\\n && useradd --create-home --uid 10001 agent \\\n && mkdir -p /data && chown agent /data\n", strings.Join(install, " "))
		for _, u := range unknown {
			fmt.Fprintf(&b, "# TODO: install %q, which the agent is allowed to run.\n", u)
		}
		b.WriteString("USER agent\n")
	} else {
		b.WriteString("FROM busybox:1.36 AS fs\n")
		b.WriteString("RUN mkdir -p /data && chown 65532:65532 /data\n\n")
		b.WriteString("# Static binary: no shell, no package manager, CA certificates and time zones included.\n")
		b.WriteString("FROM gcr.io/distroless/static-debian12:nonroot\n")
		b.WriteString("COPY --from=fs --chown=65532:65532 /data /data\n")
	}
	b.WriteString("\nARG TARGETARCH\n")
	b.WriteString("COPY --chmod=0755 docker/linux-${TARGETARCH}/agent /usr/local/bin/agent\n\n")
	b.WriteString("# Memory, sessions, traces and the workspace live under /data (relative paths in the spec).\n")
	b.WriteString("WORKDIR /data\n")
	b.WriteString("VOLUME [\"/data\"]\n")
	fmt.Fprintf(&b, "ENV PORT=%d\n", port)
	fmt.Fprintf(&b, "EXPOSE %d\n", port)
	b.WriteString("HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \\\n  CMD [\"/usr/local/bin/agent\", \"--healthcheck\"]\n")
	b.WriteString("ENTRYPOINT [\"/usr/local/bin/agent\", \"--host\", \"0.0.0.0\"]\n")
	return b.String()
}

// Compose renders a docker-compose.yml that passes secrets through from the host.
func Compose(s *spec.Spec) string {
	var b strings.Builder
	img := imageName(s)
	fmt.Fprintf(&b, "# docker compose up --build   →   http://localhost:%d/v1\n", s.Server.Port)
	b.WriteString("services:\n")
	fmt.Fprintf(&b, "  %s:\n", img)
	b.WriteString("    build: .\n")
	fmt.Fprintf(&b, "    image: %s:latest\n", img)
	fmt.Fprintf(&b, "    ports:\n      - \"%d:%d\"\n", s.Server.Port, s.Server.Port)
	if envs := envVars(s); len(envs) > 0 {
		b.WriteString("    environment:\n")
		b.WriteString("      # Passed through from your shell or a .env file next to this compose file.\n")
		for _, e := range envs {
			fmt.Fprintf(&b, "      - %s\n", e)
		}
	}
	fmt.Fprintf(&b, "    volumes:\n      - %s-data:/data\n", img)
	if usesWorkspace(s) {
		fmt.Fprintf(&b, "      # Give the agent a project to work on (its workspace root is %s):\n", s.Permissions.WorkspaceRoot)
		b.WriteString("      # - ./my-project:/data/workspace\n")
	}
	b.WriteString("    restart: unless-stopped\n")
	fmt.Fprintf(&b, "volumes:\n  %s-data:\n", img)
	return b.String()
}

// usesWorkspace reports whether the agent reads or writes files in its workspace.
func usesWorkspace(s *spec.Spec) bool {
	for _, b := range s.Tools.Builtins {
		switch b {
		case "read_file", "list_dir", "write_file", "shell":
			return true
		}
	}
	return false
}

func dockerignore(s *spec.Spec, goos, goarch string) string {
	return "# Only docker/ is needed in the build context.\n" + Filename(s, goos, goarch) + "\nREADME.md\n"
}

// BundleReadme explains how to run the bundle natively and with Docker.
func BundleReadme(s *spec.Spec, t Target) string {
	var b strings.Builder
	bin := Filename(s, t.OS, t.Arch)
	base := fmt.Sprintf("http://localhost:%d/v1", s.Server.Port)
	envs := envVars(s)
	fmt.Fprintf(&b, "# %s\n\n", s.Name)
	if s.Description != "" {
		b.WriteString(s.Description + "\n\n")
	}
	fmt.Fprintf(&b, "An OpenHarness agent that serves an OpenAI-compatible API (`POST /v1/chat/completions`, model `%s`).\n\n", s.Server.ModelID)
	b.WriteString("## Contents\n\n")
	fmt.Fprintf(&b, "- `%s`: native binary for %s\n", bin, t.Label)
	b.WriteString("- `docker/linux-amd64/agent`, `docker/linux-arm64/agent`: Linux binaries used by the image\n")
	b.WriteString("- `Dockerfile`, `docker-compose.yml`: run the agent in a container\n\n")
	if len(envs) > 0 {
		b.WriteString("## Environment variables\n\n")
		for _, e := range envs {
			fmt.Fprintf(&b, "- `%s`\n", e)
		}
		b.WriteString("\n")
	}
	b.WriteString("## Run with Docker\n\n```bash\n")
	for _, e := range envs {
		fmt.Fprintf(&b, "export %s=...\n", e)
	}
	b.WriteString("docker compose up --build\n```\n\n")
	b.WriteString("or without Compose:\n\n```bash\n")
	fmt.Fprintf(&b, "docker build -t %s .\n", imageName(s))
	runEnv := ""
	for _, e := range envs {
		runEnv += " -e " + e
	}
	fmt.Fprintf(&b, "docker run --rm -p %d:%d%s -v %s-data:/data %s\n```\n\n", s.Server.Port, s.Server.Port, runEnv, imageName(s), imageName(s))
	b.WriteString("## Run natively\n\n")
	switch t.OS {
	case "windows":
		b.WriteString("```powershell\n")
		for _, e := range envs {
			fmt.Fprintf(&b, "$env:%s = \"...\"\n", e)
		}
		fmt.Fprintf(&b, ".\\%s\n```\n\n", bin)
	default:
		b.WriteString("```bash\n")
		if t.OS == "darwin" {
			fmt.Fprintf(&b, "xattr -d com.apple.quarantine ./%s   # only if macOS blocks it\n", bin)
		}
		for _, e := range envs {
			fmt.Fprintf(&b, "export %s=...\n", e)
		}
		fmt.Fprintf(&b, "./%s\n```\n\n", bin)
	}
	b.WriteString("## Call it\n\n```bash\n")
	fmt.Fprintf(&b, "curl %s/chat/completions -H 'Content-Type: application/json' \\\n  -d '{\"model\":\"%s\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}]}'\n```\n\n", base, s.Server.ModelID)
	b.WriteString("Flags: `--port`, `--host`, `--spec file.json`, `--print-spec`, `--check`, `--healthcheck`, `--version`.\n")
	return b.String()
}
