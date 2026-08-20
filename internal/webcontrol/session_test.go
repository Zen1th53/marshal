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

func TestT173OneTimeCodeAndSessionLifecycle(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. Generate one-time login code
	code, err := server.Sessions().CreateOneTimeCode("operator-alice", "admin")
	if err != nil {
		t.Fatalf("CreateOneTimeCode: %v", err)
	}
	if len(code) < 16 {
		t.Fatalf("code too short: %s", code)
	}

	// 2. Redeem code via POST /api/v1/auth/login
	loginPayload, _ := json.Marshal(map[string]string{"code": code})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginPayload))
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d (%s)", w.Code, w.Body.String())
	}

	// Check HttpOnly, SameSite cookie
	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == webcontrol.SessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("missing session cookie in login response")
	}
	if !sessionCookie.HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}
	if sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("session cookie must be SameSite=Lax")
	}

	// 3. STRICT INVARIANT: Reusing the same code must FAIL (single-use)
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginPayload))
	server.Handler().ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for reused code, got: %d", w2.Code)
	}

	// 4. Authenticated request to /api/v1/auth/me with session cookie
	reqMe := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	reqMe.AddCookie(sessionCookie)
	wMe := httptest.NewRecorder()
	server.Handler().ServeHTTP(wMe, reqMe)
	if wMe.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /auth/me, got: %d (%s)", wMe.Code, wMe.Body.String())
	}

	var authUser webcontrol.AuthUserDTO
	if err := json.NewDecoder(wMe.Body).Decode(&authUser); err != nil {
		t.Fatalf("Decode authUser: %v", err)
	}
	if authUser.PrincipalID != "operator-alice" || authUser.Role != "admin" {
		t.Fatalf("unexpected authUser: %+v", authUser)
	}

	// 5. Logout revokes session
	reqLogout := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	reqLogout.AddCookie(sessionCookie)
	wLogout := httptest.NewRecorder()
	server.Handler().ServeHTTP(wLogout, reqLogout)
	if wLogout.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for logout, got: %d", wLogout.Code)
	}

	// 6. Subsquent /auth/me with revoked session must be rejected (in non-loopback or explicit check)
	_, err = server.Sessions().GetSession(sessionCookie.Value)
	if err == nil {
		t.Fatal("expected session to be revoked and deleted from store")
	}
}

func TestT173SessionSecurityInvariants(t *testing.T) {
	server, _ := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)

	// Invariant: Malformed / non-existent code rejected
	loginPayload, _ := json.Marshal(map[string]string{"code": "non-existent-code-xyz"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginPayload))
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for bad code, got: %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "stack trace") {
		t.Fatal("response should never contain stack trace")
	}
}
