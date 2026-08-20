package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT218ProductionAssetServingAndSPAFallback(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. Root / serves index.html
	reqRoot := httptest.NewRequest(http.MethodGet, "/", nil)
	wRoot := httptest.NewRecorder()
	server.Handler().ServeHTTP(wRoot, reqRoot)

	if wRoot.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for root /, got: %d", wRoot.Code)
	}
	body := wRoot.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") && !strings.Contains(body, "<html") {
		t.Fatalf("expected index.html response, got: %s", body)
	}

	// 2. SPA route /tasks/TSK-001 falls back to index.html
	reqSPA := httptest.NewRequest(http.MethodGet, "/tasks/TSK-001", nil)
	wSPA := httptest.NewRecorder()
	server.Handler().ServeHTTP(wSPA, reqSPA)

	if wSPA.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for SPA fallback route, got: %d", wSPA.Code)
	}
	spaBody := wSPA.Body.String()
	if !strings.Contains(spaBody, "<!DOCTYPE html>") && !strings.Contains(spaBody, "<html") {
		t.Fatalf("expected SPA fallback to serve index.html, got: %s", spaBody)
	}

	// 3. CRITICAL INVARIANT: /api/ nonexistent routes must NOT be swallowed by SPA fallback
	reqAPI404 := httptest.NewRequest(http.MethodGet, "/api/v1/nonexistent_route_probe", nil)
	wAPI404 := httptest.NewRecorder()
	server.Handler().ServeHTTP(wAPI404, reqAPI404)

	if wAPI404.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found for nonexistent API endpoint, got: %d", wAPI404.Code)
	}

	var errEnv webcontrol.ErrorEnvelope
	if err := json.NewDecoder(wAPI404.Body).Decode(&errEnv); err != nil {
		t.Fatalf("expected JSON error envelope for API 404, got: %v", err)
	}
	if errEnv.Error.Code != "api_endpoint_not_found" {
		t.Fatalf("unexpected API 404 code: %s", errEnv.Error.Code)
	}
}
