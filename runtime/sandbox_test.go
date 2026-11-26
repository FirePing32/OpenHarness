package runtime

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBlockPrivateNetworks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	BlockPrivateNetworks = true
	defer func() { BlockPrivateNetworks = false }()
	_, err := newHTTPClient(0).Get(srv.URL)
	if !errors.Is(err, errBlockedAddr) {
		t.Fatalf("loopback not blocked: %v", err)
	}
	for ip, want := range map[string]bool{"8.8.8.8": true, "169.254.169.254": false, "10.1.2.3": false, "100.64.0.1": false, "::1": false} {
		if got := guardControl("tcp", net.JoinHostPort(ip, "80"), nil) == nil; got != want {
			t.Errorf("%s allowed=%v want %v", ip, got, want)
		}
	}
}

func TestLookupEnvHook(t *testing.T) {
	t.Setenv("OH_SECRET", "s3cret")
	old := LookupEnv
	defer func() { LookupEnv = old }()
	if expandEnv("Bearer ${OH_SECRET}") != "Bearer s3cret" {
		t.Fatal("default should expand")
	}
	LookupEnv = func(string) string { return "" }
	if got := expandEnv("Bearer ${OH_SECRET}"); got != "Bearer " {
		t.Fatalf("sandboxed expand leaked: %q", got)
	}
}
