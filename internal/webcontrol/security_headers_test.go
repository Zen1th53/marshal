package webcontrol_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT175SecurityHeadersGoldenSuite(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	h := w.Header()

	// Invariant 1: Clickjacking / Framing defense
	if h.Get("X-Frame-Options") != "DENY" {
		t.Errorf("expected X-Frame-Options: DENY, got: %s", h.Get("X-Frame-Options"))
	}

	// Invariant 2: MIME-sniffing prevention
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("expected X-Content-Type-Options: nosniff, got: %s", h.Get("X-Content-Type-Options"))
	}

	// Invariant 3: Referrer policy
	if h.Get("Referrer-Policy") != "strict-origin-when-cross-origin" {
		t.Errorf("expected Referrer-Policy: strict-origin-when-cross-origin, got: %s", h.Get("Referrer-Policy"))
	}

	// Invariant 4: Strict CSP directives
	csp := h.Get("Content-Security-Policy")
	requiredDirectives := []string{
		"default-src 'self'",
		"script-src 'self'",
		"connect-src 'self'",
		"object-src 'none'",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}
	for _, dir := range requiredDirectives {
		if !strings.Contains(csp, dir) {
			t.Errorf("CSP missing required directive: %s (actual: %s)", dir, csp)
		}
	}

	// Invariant 5: Strict prohibition of unsafe-eval and wildcards in CSP
	if strings.Contains(csp, "unsafe-eval") {
		t.Errorf("CSP must never permit unsafe-eval: %s", csp)
	}
	if strings.Contains(csp, "*") {
		t.Errorf("CSP must not contain wildcard origins: %s", csp)
	}

	// Invariant 6: Cross-Origin isolation headers
	if h.Get("Cross-Origin-Opener-Policy") != "same-origin" {
		t.Errorf("expected COOP: same-origin, got: %s", h.Get("Cross-Origin-Opener-Policy"))
	}
	if h.Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Errorf("expected CORP: same-origin, got: %s", h.Get("Cross-Origin-Resource-Policy"))
	}
}
