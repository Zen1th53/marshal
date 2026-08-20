package webcontrol_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT179CorrelationIDGenerationAndSanitization(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. Safe valid correlation ID is preserved
	validID := "req-client-12345678"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	req.Header.Set("X-Correlation-ID", validID)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	if w.Header().Get("X-Correlation-ID") != validID {
		t.Fatalf("expected preserved correlation ID %s, got %s", validID, w.Header().Get("X-Correlation-ID"))
	}

	// 2. Malicious forged header with CRLF / HTML injection is sanitized & replaced
	maliciousID := "req-evil\r\nSet-Cookie: stolen=1<script>alert(1)</script>"
	reqMalicious := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	reqMalicious.Header.Set("X-Correlation-ID", maliciousID)
	wMalicious := httptest.NewRecorder()
	server.Handler().ServeHTTP(wMalicious, reqMalicious)

	sanitizedResp := wMalicious.Header().Get("X-Correlation-ID")
	if strings.Contains(sanitizedResp, "<script>") || strings.Contains(sanitizedResp, "\r\n") {
		t.Fatalf("sanitization failed, response contains malicious payload: %s", sanitizedResp)
	}
	if !strings.HasPrefix(sanitizedResp, "req-") {
		t.Fatalf("expected newly generated safe correlation ID, got %s", sanitizedResp)
	}
}
