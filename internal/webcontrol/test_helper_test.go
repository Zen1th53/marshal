package webcontrol_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

type testClient struct {
	server  *webcontrol.Server
	cookie  *http.Cookie
	csrf    string
	user    webcontrol.AuthUserDTO
}

func newAuthenticatedTestClient(t *testing.T, role string) *testClient {
	t.Helper()
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	code, err := server.Sessions().CreateOneTimeCode("test-operator", role)
	if err != nil {
		t.Fatalf("CreateOneTimeCode: %v", err)
	}

	loginPayload, _ := json.Marshal(map[string]string{"code": code})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginPayload))
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Login failed: %d (%s)", w.Code, w.Body.String())
	}

	var authUser webcontrol.AuthUserDTO
	_ = json.NewDecoder(w.Body).Decode(&authUser)

	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == webcontrol.SessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie found in login response")
	}

	// Fetch CSRF token
	reqCSRF := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	reqCSRF.AddCookie(sessionCookie)
	wCSRF := httptest.NewRecorder()
	server.Handler().ServeHTTP(wCSRF, reqCSRF)

	var csrfResp map[string]string
	_ = json.NewDecoder(wCSRF.Body).Decode(&csrfResp)

	return &testClient{
		server: server,
		cookie: sessionCookie,
		csrf:   csrfResp["csrf_token"],
		user:   authUser,
	}
}

func (c *testClient) Server() *webcontrol.Server {
	return c.server
}

func (c *testClient) Sessions() *webcontrol.SessionStore {
	return c.server.Sessions()
}

func (c *testClient) Do(req *http.Request) *httptest.ResponseRecorder {
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	if c.csrf != "" && req.Method != http.MethodGet && req.Method != http.MethodHead && req.Method != http.MethodOptions {
		if req.Header.Get("X-CSRF-Token") == "" {
			req.Header.Set("X-CSRF-Token", c.csrf)
		}
	}
	w := httptest.NewRecorder()
	c.server.Handler().ServeHTTP(w, req)
	return w
}
