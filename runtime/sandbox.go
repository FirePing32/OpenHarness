package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"
)

// Process-wide sandbox switches. A binary run by its owner leaves them at
// their permissive defaults. The public builder tightens them for previews,
// where the spec comes from an untrusted visitor.
var (
	// LookupEnv resolves environment references in a spec: model API key env
	// vars and ${VAR} in tool/MCP headers. The public builder replaces it so a
	// visitor's spec cannot read the server's secrets.
	LookupEnv = os.Getenv

	// BlockPrivateNetworks makes every outbound HTTP connection refuse
	// loopback, private, link-local and other non-public addresses (SSRF guard).
	BlockPrivateNetworks bool
)

func expandEnv(s string) string { return os.Expand(s, LookupEnv) }

var errBlockedAddr = errors.New("destination address is not allowed")

// publicIP reports whether ip is routable on the public internet.
func publicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	return !cgnat.Contains(ip)
}

// guardControl runs after DNS resolution, so rebinding tricks are covered too.
func guardControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !publicIP(ip) {
		return fmt.Errorf("%w: %s", errBlockedAddr, host)
	}
	return nil
}

// newHTTPClient returns the client every outbound runtime call uses.
func newHTTPClient(timeout time.Duration) *http.Client {
	if !BlockPrivateNetworks {
		return &http.Client{Timeout: timeout}
	}
	d := &net.Dialer{Timeout: 15 * time.Second, Control: guardControl}
	tr := &http.Transport{
		DialContext:           func(ctx context.Context, n, a string) (net.Conn, error) { return d.DialContext(ctx, n, a) },
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
		MaxIdleConns:          50,
		IdleConnTimeout:       90 * time.Second,
	}
	return &http.Client{Timeout: timeout, Transport: tr}
}
