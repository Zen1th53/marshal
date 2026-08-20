package webcontrol_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT174CSRFAndOriginProtection(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. Establish session
	code, _ := server.Sessions().CreateOneTimeCode("operator", "admin")
	loginPayload, _ := json.Marshal(map[string]string{"code": code})
	reqLogin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginPayload))
	wLogin := httptest.NewRecorder()
	server.Handler().ServeHTTP(wLogin, reqLogin)

	sessionCookie := wLogin.Result().Cookies()[0]

	// 2. Fetch valid CSRF token
	reqCSRF := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	reqCSRF.AddCookie(sessionCookie)
	wCSRF := httptest.NewRecorder()
	server.Handler().ServeHTTP(wCSRF, reqCSRF)

	var csrfResp map[string]string
	_ = json.NewDecoder(wCSRF.Body).Decode(&csrfResp)
	csrfToken := csrfResp["csrf_token"]
	if csrfToken == "" {
		t.Fatal("expected non-empty csrf_token")
	}

	// 3. STRICT INVARIANT: Mutation with missing X-CSRF-Token must be rejected (403)
	reqNoCSRF := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	reqNoCSRF.AddCookie(sessionCookie)
	wNoCSRF := httptest.NewRecorder()
	server.Handler().ServeHTTP(wNoCSRF, reqNoCSRF)
	if wNoCSRF.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for missing CSRF token, got: %d", wNoCSRF.Code)
	}

	// 4. STRICT INVARIANT: Mutation with invalid X-CSRF-Token must be rejected (403)
	reqBadCSRF := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	reqBadCSRF.AddCookie(sessionCookie)
	reqBadCSRF.Header.Set("X-CSRF-Token", "forged-csrf-token-12345")
	wBadCSRF := httptest.NewRecorder()
	server.Handler().ServeHTTP(wBadCSRF, reqBadCSRF)
	if wBadCSRF.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for invalid CSRF token, got: %d", wBadCSRF.Code)
	}

	// 5. STRICT INVARIANT: Cross-origin mutation from evil.com must be rejected (403)
	reqEvilOrigin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	reqEvilOrigin.AddCookie(sessionCookie)
	reqEvilOrigin.Header.Set("Origin", "https://evil-attacker.com")
	reqEvilOrigin.Header.Set("X-CSRF-Token", csrfToken)
	wEvilOrigin := httptest.NewRecorder()
	server.Handler().ServeHTTP(wEvilOrigin, reqEvilOrigin)
	if wEvilOrigin.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for evil.com Origin, got: %d", wEvilOrigin.Code)
	}

	// 6. Valid mutation with matching CSRF token and valid Origin/Host passes
	reqValid := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	reqValid.AddCookie(sessionCookie)
	reqValid.Header.Set("X-CSRF-Token", csrfToken)
	wValid := httptest.NewRecorder()
	server.Handler().ServeHTTP(wValid, reqValid)
	if wValid.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for valid CSRF mutation, got: %d", wValid.Code)
	}
}

func TestT174SecurityHeaders(t *testing.T) {
	server, _ := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	h := w.Header()
	if h.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("expected X-Frame-Options: DENY, got: %s", h.Get("X-Frame-Options"))
	}
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("expected X-Content-Type-Options: nosniff, got: %s", h.Get("X-Content-Type-Options"))
	}
	csp := h.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || strings.Contains(csp, "https://cdn.") {
		t.Fatalf("unsafe CSP: %s", csp)
	}
}
