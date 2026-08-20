package webcontrol_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT176RoleAuthorityMatrix(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. Create a viewer session
	codeViewer, _ := server.Sessions().CreateOneTimeCode("viewer-bob", "viewer")
	loginPayload, _ := json.Marshal(map[string]string{"code": codeViewer})
	reqLogin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginPayload))
	wLogin := httptest.NewRecorder()
	server.Handler().ServeHTTP(wLogin, reqLogin)

	viewerCookie := wLogin.Result().Cookies()[0]

	// 2. Fetch valid CSRF token for viewer
	reqCSRF := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	reqCSRF.AddCookie(viewerCookie)
	wCSRF := httptest.NewRecorder()
	server.Handler().ServeHTTP(wCSRF, reqCSRF)
	var csrfResp map[string]string
	_ = json.NewDecoder(wCSRF.Body).Decode(&csrfResp)
	csrfToken := csrfResp["csrf_token"]

	// 3. Test protected handler requiring release.approve authority
	protectedHandler := server.RequireAuthority("release.approve", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"approved"}`))
	})

	// 4. Viewer attempt to call release.approve must be rejected (403 Forbidden)
	reqProtected := httptest.NewRequest(http.MethodPost, "/api/v1/gates/approve", nil)
	reqProtected.AddCookie(viewerCookie)
	reqProtected.Header.Set("X-CSRF-Token", csrfToken)
	wProtected := httptest.NewRecorder()
	protectedHandler.ServeHTTP(wProtected, reqProtected)

	if wProtected.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for viewer calling privileged action, got: %d", wProtected.Code)
	}

	// 5. Admin session calling release.approve must succeed (200 OK)
	codeAdmin, _ := server.Sessions().CreateOneTimeCode("admin-alice", "admin")
	loginAdminPayload, _ := json.Marshal(map[string]string{"code": codeAdmin})
	reqAdminLogin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginAdminPayload))
	wAdminLogin := httptest.NewRecorder()
	server.Handler().ServeHTTP(wAdminLogin, reqAdminLogin)
	adminCookie := wAdminLogin.Result().Cookies()[0]

	reqAdminAction := httptest.NewRequest(http.MethodPost, "/api/v1/gates/approve", nil)
	reqAdminAction.AddCookie(adminCookie)
	reqAdminAction.Header.Set("X-CSRF-Token", csrfToken)
	wAdminAction := httptest.NewRecorder()
	protectedHandler.ServeHTTP(wAdminAction, reqAdminAction)

	if wAdminAction.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for admin calling privileged action, got: %d", wAdminAction.Code)
	}
}

func TestT176RevisionConcurrencyValidation(t *testing.T) {
	// CAS revision check: expected matches current -> PASS
	if err := webcontrol.ValidateRevision(5, 5); err != nil {
		t.Fatalf("expected match, got: %v", err)
	}

	// CAS revision check: expected does not match current -> ErrConflict
	if err := webcontrol.ValidateRevision(5, 4); err != webcontrol.ErrConflict {
		t.Fatalf("expected ErrConflict for stale revision, got: %v", err)
	}
}
