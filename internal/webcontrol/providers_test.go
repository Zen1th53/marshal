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

func TestT196ProviderInventoryAndRouter(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. Get Provider Inventory
	reqList := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	wList := httptest.NewRecorder()
	server.Handler().ServeHTTP(wList, reqList)

	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", wList.Code)
	}

	rawBody := wList.Body.String()

	// 2. Security Invariant: Zero Secret Leakage Scan
	forbiddenSubstrings := []string{"sk-", "api_key", "bearer", "password", "client_secret", "export OPENAI_API_KEY"}
	for _, forbidden := range forbiddenSubstrings {
		if strings.Contains(strings.ToLower(rawBody), forbidden) {
			t.Fatalf("CRITICAL SECURITY DEFECT: found secret substring %q in provider response: %s", forbidden, rawBody)
		}
	}

	var resp webcontrol.ProviderInventoryResponseDTO
	_ = json.NewDecoder(wList.Body).Decode(&resp)

	if len(resp.Providers) < 4 || len(resp.RoutingDecisions) < 4 {
		t.Fatalf("expected providers and routing decisions, got: %+v", resp)
	}

	// 3. Router Override with Auth
	code, _ := server.Sessions().CreateOneTimeCode("admin-zen1th", "admin")
	loginPayload, _ := json.Marshal(map[string]string{"code": code})
	reqLogin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginPayload))
	wLogin := httptest.NewRecorder()
	server.Handler().ServeHTTP(wLogin, reqLogin)
	cookie := wLogin.Result().Cookies()[0]

	reqCSRF := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	reqCSRF.AddCookie(cookie)
	wCSRF := httptest.NewRecorder()
	server.Handler().ServeHTTP(wCSRF, reqCSRF)
	var csrfResp map[string]string
	_ = json.NewDecoder(wCSRF.Body).Decode(&csrfResp)
	csrfToken := csrfResp["csrf_token"]

	overridePayload := webcontrol.MutationEnvelope[webcontrol.RouterOverridePayload]{
		Payload: webcontrol.RouterOverridePayload{
			Intent:   "planning",
			ModelID:  "claude-3-7-sonnet",
			IsPinned: true,
		},
	}
	bodyBytes, _ := json.Marshal(overridePayload)

	reqOverride := httptest.NewRequest(http.MethodPost, "/api/v1/providers/router/override", bytes.NewReader(bodyBytes))
	reqOverride.Header.Set("Content-Type", "application/json")
	reqOverride.Header.Set("X-CSRF-Token", csrfToken)
	reqOverride.AddCookie(cookie)
	wOverride := httptest.NewRecorder()
	server.Handler().ServeHTTP(wOverride, reqOverride)

	if wOverride.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for router override, got: %d: %s", wOverride.Code, wOverride.Body.String())
	}
}
