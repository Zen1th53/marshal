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

func TestT199GlobalAuditTimelineAndExport(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. List Audit Events
	reqList := httptest.NewRequest(http.MethodGet, "/api/v1/audit/events", nil)
	wList := httptest.NewRecorder()
	server.Handler().ServeHTTP(wList, reqList)

	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", wList.Code)
	}

	var listResp webcontrol.AuditEventsResponseDTO
	_ = json.NewDecoder(wList.Body).Decode(&listResp)

	if listResp.TotalCount < 3 || len(listResp.Events) < 3 {
		t.Fatalf("expected at least 3 audit events, got: %d", listResp.TotalCount)
	}

	// 2. Filter by outcome=denied
	reqDenied := httptest.NewRequest(http.MethodGet, "/api/v1/audit/events?outcome=denied", nil)
	wDenied := httptest.NewRecorder()
	server.Handler().ServeHTTP(wDenied, reqDenied)

	var deniedResp webcontrol.AuditEventsResponseDTO
	_ = json.NewDecoder(wDenied.Body).Decode(&deniedResp)

	if len(deniedResp.Events) != 1 || deniedResp.Events[0].Outcome != "denied" {
		t.Fatalf("expected 1 denied audit event, got: %d", len(deniedResp.Events))
	}

	// 3. Unauthenticated/Invalid Session Export -> 401
	reqUnauth := httptest.NewRequest(http.MethodGet, "/api/v1/audit/export", nil)
	reqUnauth.AddCookie(&http.Cookie{Name: webcontrol.SessionCookieName, Value: "invalid-expired-session"})
	wUnauth := httptest.NewRecorder()
	server.Handler().ServeHTTP(wUnauth, reqUnauth)

	if wUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for unauth export, got: %d", wUnauth.Code)
	}

	// 4. Authenticated Export
	code, _ := server.Sessions().CreateOneTimeCode("auditor-zen1th", "admin")
	loginPayload, _ := json.Marshal(map[string]string{"code": code})
	reqLogin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginPayload))
	wLogin := httptest.NewRecorder()
	server.Handler().ServeHTTP(wLogin, reqLogin)
	cookie := wLogin.Result().Cookies()[0]

	reqExport := httptest.NewRequest(http.MethodGet, "/api/v1/audit/export", nil)
	reqExport.AddCookie(cookie)
	wExport := httptest.NewRecorder()
	server.Handler().ServeHTTP(wExport, reqExport)

	if wExport.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for auth export, got: %d", wExport.Code)
	}

	h := wExport.Header()
	if !strings.Contains(h.Get("Content-Disposition"), "attachment; filename=") {
		t.Fatalf("expected attachment header, got: %s", h.Get("Content-Disposition"))
	}
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("expected nosniff header, got: %s", h.Get("X-Content-Type-Options"))
	}
}
