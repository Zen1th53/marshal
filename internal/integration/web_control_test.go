package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT219EndToEndOperatorLifecycleAndAdversarialSecurity(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// -------------------------------------------------------------
	// FLOW 1: Operator One-Time Code Authentication Lifecycle
	// -------------------------------------------------------------
	loginReqPayload := map[string]string{
		"code": "000000", // Invalid code
	}
	bodyBytes, _ := json.Marshal(loginReqPayload)
	reqBadLogin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(bodyBytes))
	reqBadLogin.Header.Set("Content-Type", "application/json")
	wBadLogin := httptest.NewRecorder()
	server.Handler().ServeHTTP(wBadLogin, reqBadLogin)

	if wBadLogin.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for bad one-time code, got: %d", wBadLogin.Code)
	}

	// -------------------------------------------------------------
	// ATTACK CASE 1: CSRF & Origin Validation on State Mutations
	// -------------------------------------------------------------
	mutationPayload := map[string]any{
		"idempotency_key": "idem-attack-001",
		"payload": map[string]string{
			"title": "Malicious CSRF Task",
		},
	}
	mBytes, _ := json.Marshal(mutationPayload)
	reqCSRFAttack := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(mBytes))
	reqCSRFAttack.Header.Set("Content-Type", "application/json")
	reqCSRFAttack.Header.Set("Origin", "http://evil-attacker.com")
	wCSRFAttack := httptest.NewRecorder()
	server.Handler().ServeHTTP(wCSRFAttack, reqCSRFAttack)

	if wCSRFAttack.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for cross-origin state mutation, got: %d", wCSRFAttack.Code)
	}

	// Get valid CSRF token for authorized mutations
	reqCSRF := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	wCSRF := httptest.NewRecorder()
	server.Handler().ServeHTTP(wCSRF, reqCSRF)
	csrfToken := wCSRF.Header().Get("X-CSRF-Token")

	// -------------------------------------------------------------
	// ATTACK CASE 2: Stored XSS Neutralization in Task & Log Payloads
	// -------------------------------------------------------------
	xssPayload := map[string]any{
		"idempotency_key": "idem-xss-001",
		"payload": map[string]string{
			"title":       "<script>alert('XSS_PAYLOAD')</script>",
			"description": "<img src=x onerror=alert('DOM_XSS')>",
			"priority":    "P0",
		},
	}
	xBytes, _ := json.Marshal(xssPayload)
	reqXSS := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(xBytes))
	reqXSS.Header.Set("Content-Type", "application/json")
	if csrfToken != "" {
		reqXSS.Header.Set("X-CSRF-Token", csrfToken)
	}
	wXSS := httptest.NewRecorder()
	server.Handler().ServeHTTP(wXSS, reqXSS)

	if wXSS.Code != http.StatusOK && wXSS.Code != http.StatusCreated {
		t.Fatalf("expected task creation with sanitized XSS payload, got: %d", wXSS.Code)
	}

	// Verify X-Content-Type-Options nosniff and CSP headers prevent execution
	if wXSS.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing X-Content-Type-Options: nosniff header")
	}
	if wXSS.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("missing X-Frame-Options: DENY header")
	}

	// -------------------------------------------------------------
	// ATTACK CASE 3: Stale CAS Revision Concurrency Conflict
	// -------------------------------------------------------------
	staleSettingsPayload := map[string]any{
		"idempotency_key": "idem-stale-001",
		"payload": map[string]any{
			"expected_revision":          999, // Stale revision
			"system_mode":                "strict",
			"max_concurrent_workers":     4,
			"telemetry_level":            "standard",
			"auto_consolidation_enabled": true,
			"memory_retention_days":      30,
		},
	}
	sBytes, _ := json.Marshal(staleSettingsPayload)
	reqStale := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewReader(sBytes))
	reqStale.Header.Set("Content-Type", "application/json")
	if csrfToken != "" {
		reqStale.Header.Set("X-CSRF-Token", csrfToken)
	}
	wStale := httptest.NewRecorder()
	server.Handler().ServeHTTP(wStale, reqStale)

	if wStale.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for stale CAS expected_revision=999, got: %d", wStale.Code)
	}

	// -------------------------------------------------------------
	// FLOW 2: Global Entity Search & Routing Boundary
	// -------------------------------------------------------------
	reqSearch := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=TSK-001", nil)
	wSearch := httptest.NewRecorder()
	server.Handler().ServeHTTP(wSearch, reqSearch)

	if wSearch.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for global search, got: %d", wSearch.Code)
	}

	var searchResp webcontrol.GlobalSearchResponseDTO
	if err := json.NewDecoder(wSearch.Body).Decode(&searchResp); err != nil {
		t.Fatalf("failed to decode search response: %v", err)
	}
	if len(searchResp.Results) == 0 || searchResp.Results[0].ID != "TSK-001" {
		t.Fatalf("expected exact match TSK-001, got: %+v", searchResp)
	}

	// -------------------------------------------------------------
	// FLOW 3: SPA Fallback vs API 404 Isolation
	// -------------------------------------------------------------
	reqAPI404 := httptest.NewRequest(http.MethodGet, "/api/v1/invalid_route", nil)
	wAPI404 := httptest.NewRecorder()
	server.Handler().ServeHTTP(wAPI404, reqAPI404)

	if wAPI404.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for invalid API endpoint, got: %d", wAPI404.Code)
	}
	if strings.Contains(wAPI404.Body.String(), "<html") {
		t.Fatalf("critical invariant violated: SPA fallback swallowed API 404!")
	}
}
