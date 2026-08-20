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
	client := newAuthenticatedTestClient(t, "admin")

	// 1. Get Provider Inventory
	reqList := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	wList := client.Do(reqList)

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
	code, _ := client.Sessions().CreateOneTimeCode("admin-zen1th", "admin")
	loginPayload, _ := json.Marshal(map[string]string{"code": code})
	reqLogin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginPayload))
	wLogin := client.Do(reqLogin)
	cookie := wLogin.Result().Cookies()[0]

	reqCSRF := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	reqCSRF.AddCookie(cookie)
	wCSRF := client.Do(reqCSRF)
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
	wOverride := client.Do(reqOverride)

	if wOverride.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for router override, got: %d: %s", wOverride.Code, wOverride.Body.String())
	}
}
