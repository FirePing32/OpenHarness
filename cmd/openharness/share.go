package main

import (
	"bytes"
	"html"
	"net/http"
	"os"
	"regexp"
	"strings"

	"openharness/web"
)

var hostRe = regexp.MustCompile(`^[A-Za-z0-9.\-]+(:[0-9]{1,5})?$`)

// baseURL is the absolute origin used in link-preview tags. Previews
// (WhatsApp, Slack, X) need absolute URLs, and the domain is only known at
// run time: OPENHARNESS_PUBLIC_URL wins, then the proxy's X-Forwarded-*
// headers (when trusted), then the request itself.
func (a *app) baseURL(r *http.Request) string {
	if u := strings.TrimRight(os.Getenv("OPENHARNESS_PUBLIC_URL"), "/"); u != "" {
		return u
	}
	scheme, host := "http", r.Host
	if r.TLS != nil {
		scheme = "https"
	}
	if a.cfg.trustProxy {
		if p := r.Header.Get("X-Forwarded-Proto"); p == "https" || p == "http" {
			scheme = p
		}
		if h := r.Header.Get("X-Forwarded-Host"); h != "" {
			host = strings.TrimSpace(strings.Split(h, ",")[0])
		}
	}
	if !hostRe.MatchString(host) {
		return "" // unexpected Host header: fall back to relative URLs
	}
	return scheme + "://" + host
}

// index serves the builder page with link-preview URLs made absolute.
func (a *app) index(w http.ResponseWriter, r *http.Request) {
	page, err := web.FS.ReadFile("index.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	page = bytes.ReplaceAll(page, []byte("__BASE_URL__"), []byte(html.EscapeString(a.baseURL(r))))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(page)
}
