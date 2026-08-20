package web_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT220WebControlUltimateConformanceGate(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. Loopback Invariant Verification
	if !server.IsLoopback() {
		t.Fatalf("expected loopback bind to be true")
	}

	// Authenticate session for protected endpoints
	code, err := server.Sessions().CreateOneTimeCode("conformance-admin", "admin")
	if err != nil {
		t.Fatalf("CreateOneTimeCode: %v", err)
	}
	loginPayload, _ := json.Marshal(map[string]string{"code": code})
	reqLogin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginPayload))
	wLogin := httptest.NewRecorder()
	server.Handler().ServeHTTP(wLogin, reqLogin)
	if wLogin.Code != http.StatusOK {
		t.Fatalf("Login failed: %d", wLogin.Code)
	}
	sessionCookie := wLogin.Result().Cookies()[0]

	// 2. Doctor Health Diagnostic Preflight (Public)
	reqDoctor := httptest.NewRequest(http.MethodGet, "/api/v1/health/doctor", nil)
	wDoctor := httptest.NewRecorder()
	server.Handler().ServeHTTP(wDoctor, reqDoctor)

	if wDoctor.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for doctor diagnostics, got: %d", wDoctor.Code)
	}

	// 3. Security Policy & Gates Inspector (Authenticated)
	reqPolicy := httptest.NewRequest(http.MethodGet, "/api/v1/security/policy", nil)
	reqPolicy.AddCookie(sessionCookie)
	wPolicy := httptest.NewRecorder()
	server.Handler().ServeHTTP(wPolicy, reqPolicy)

	if wPolicy.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for security policy inspector, got: %d", wPolicy.Code)
	}

	var policyResp webcontrol.SecurityPolicyInspectorResponseDTO
	if err := json.NewDecoder(wPolicy.Body).Decode(&policyResp); err != nil {
		t.Fatalf("failed to decode policy response: %v", err)
	}
	if len(policyResp.GateRules) == 0 {
		t.Fatalf("expected active gate rules, got 0")
	}

	// 4. Release Trust & Provenance SBOM (Public)
	reqTrust := httptest.NewRequest(http.MethodGet, "/api/v1/operations/trust", nil)
	wTrust := httptest.NewRecorder()
	server.Handler().ServeHTTP(wTrust, reqTrust)

	if wTrust.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for release trust SBOM, got: %d", wTrust.Code)
	}

	// 5. Global Search Index (Authenticated)
	reqSearch := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=TSK-001", nil)
	reqSearch.AddCookie(sessionCookie)
	wSearch := httptest.NewRecorder()
	server.Handler().ServeHTTP(wSearch, reqSearch)

	if wSearch.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for global search, got: %d", wSearch.Code)
	}
}
