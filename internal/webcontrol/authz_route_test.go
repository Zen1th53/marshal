package webcontrol_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func loginAndCSRF(t *testing.T, srv *webcontrol.Server, principalID, role string) (*http.Cookie, string) {
	t.Helper()
	code, err := srv.Sessions().CreateOneTimeCode(principalID, role)
	if err != nil {
		t.Fatal(err)
	}
	loginPayload, _ := json.Marshal(map[string]string{"code": code})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginPayload))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie set on login")
	}
	cookie := cookies[0]

	reqCSRF := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	reqCSRF.AddCookie(cookie)
	wCSRF := httptest.NewRecorder()
	srv.Handler().ServeHTTP(wCSRF, reqCSRF)
	var csrfResp map[string]string
	_ = json.NewDecoder(wCSRF.Body).Decode(&csrfResp)
	return cookie, csrfResp["csrf_token"]
}

func TestWebRouteAuthorizationMatrix(t *testing.T) {
	srv, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Viewer can read tasks but cannot mutate them.
	viewerCookie, viewerCSRF := loginAndCSRF(t, srv, "viewer-bob", "viewer")

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	getReq.AddCookie(viewerCookie)
	getW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("viewer read tasks = %d, want 200", getW.Code)
	}

	postPayload := webcontrol.MutationEnvelope[map[string]any]{Payload: map[string]any{"title": "x"}}
	postBody, _ := json.Marshal(postPayload)
	postReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(postBody))
	postReq.Header.Set("Content-Type", "application/json")
	postReq.Header.Set("X-CSRF-Token", viewerCSRF)
	postReq.AddCookie(viewerCookie)
	postW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(postW, postReq)
	if postW.Code != http.StatusForbidden {
		t.Fatalf("viewer create task = %d, want 403", postW.Code)
	}

	// 2. QA lead cannot perform privileged backup/restore operations.
	qaCookie, qaCSRF := loginAndCSRF(t, srv, "auditor-gemini", "qa_lead")
	backupPayload := webcontrol.MutationEnvelope[webcontrol.CreateBackupPayload]{Payload: webcontrol.CreateBackupPayload{Label: "x"}}
	backupBody, _ := json.Marshal(backupPayload)
	backupReq := httptest.NewRequest(http.MethodPost, "/api/v1/operations/backups/create", bytes.NewReader(backupBody))
	backupReq.Header.Set("Content-Type", "application/json")
	backupReq.Header.Set("X-CSRF-Token", qaCSRF)
	backupReq.AddCookie(qaCookie)
	backupW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(backupW, backupReq)
	if backupW.Code != http.StatusForbidden {
		t.Fatalf("qa_lead create backup = %d, want 403", backupW.Code)
	}

	// 3. Admin can create a backup.
	adminCookie, adminCSRF := loginAndCSRF(t, srv, "admin-zen1th", "admin")
	adminReq := httptest.NewRequest(http.MethodPost, "/api/v1/operations/backups/create", bytes.NewReader(backupBody))
	adminReq.Header.Set("Content-Type", "application/json")
	adminReq.Header.Set("X-CSRF-Token", adminCSRF)
	adminReq.AddCookie(adminCookie)
	adminW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(adminW, adminReq)
	if adminW.Code != http.StatusOK {
		t.Fatalf("admin create backup = %d, want 200", adminW.Code)
	}

	// 4. Anonymous Loopback Rejection (P0 Security Invariant):
	// A request on loopback without an authenticated session must NOT receive implicit admin authority.
	anonGetReq := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	anonGetW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(anonGetW, anonGetReq)
	if anonGetW.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous loopback GET /api/v1/tasks = %d, want 401 Unauthorized", anonGetW.Code)
	}

	anonPostReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(postBody))
	anonPostReq.Header.Set("Content-Type", "application/json")
	anonPostW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(anonPostW, anonPostReq)
	if anonPostW.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous loopback POST /api/v1/tasks = %d, want 401 Unauthorized", anonPostW.Code)
	}

	// 5. Forged Session Cookie Rejection:
	forgedCookie := &http.Cookie{Name: "marshal_session", Value: "forged-cookie-val-9999"}
	forgedReq := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	forgedReq.AddCookie(forgedCookie)
	forgedW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(forgedW, forgedReq)
	if forgedW.Code != http.StatusUnauthorized {
		t.Fatalf("forged cookie GET /api/v1/tasks = %d, want 401 Unauthorized", forgedW.Code)
	}

	// 6. Wrong Session CSRF Token Rejection:
	wrongCSRFReq := httptest.NewRequest(http.MethodPost, "/api/v1/operations/backups/create", bytes.NewReader(backupBody))
	wrongCSRFReq.Header.Set("Content-Type", "application/json")
	wrongCSRFReq.Header.Set("X-CSRF-Token", viewerCSRF) // Viewer's CSRF token supplied with Admin's session cookie
	wrongCSRFReq.AddCookie(adminCookie)
	wrongCSRFW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(wrongCSRFW, wrongCSRFReq)
	if wrongCSRFW.Code != http.StatusForbidden {
		t.Fatalf("wrong session CSRF token = %d, want 403 Forbidden", wrongCSRFW.Code)
	}
}
