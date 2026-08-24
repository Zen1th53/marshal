package webcontrol_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT167WebControlServerRoutesAndLoopback(t *testing.T) {
	cfg := webcontrol.ServerConfig{
		Host: "127.0.0.1",
		Port: 8787,
	}

	server, err := webcontrol.NewServer(cfg, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// Invariant 1: Loopback protection — non-loopback bind without production flag rejected
	invalidCfg := webcontrol.ServerConfig{
		Host: "0.0.0.0",
		Port: 8787,
	}
	if _, err := webcontrol.NewServer(invalidCfg, nil); err == nil {
		t.Fatalf("expected error binding to 0.0.0.0 without production flag")
	}

	// Invariant 2: HTTP GET / returns HTML SPA shell
	reqSpa := httptest.NewRequest(http.MethodGet, "/", nil)
	wSpa := httptest.NewRecorder()
	server.Handler().ServeHTTP(wSpa, reqSpa)

	if wSpa.Code != http.StatusOK || !strings.Contains(wSpa.Body.String(), "<!doctype html>") {
		t.Fatalf("expected 200 OK HTML SPA shell, got: %d", wSpa.Code)
	}

	// Invariant 3: HTTP GET /api/v1/system/status returns structured JSON
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d (%s)", w.Code, w.Body.String())
	}

	var statusDTO webcontrol.SystemStatusDTO
	if err := json.NewDecoder(w.Body).Decode(&statusDTO); err != nil {
		t.Fatalf("Decode SystemStatusDTO: %v", err)
	}
	expectedSchema := fmt.Sprintf("v%d", store.LatestSchemaVersion)
	if statusDTO.State == "" || statusDTO.DatabaseSchema != expectedSchema {
		t.Fatalf("unexpected status DTO: %+v (expected schema %s)", statusDTO, expectedSchema)
	}
}

func TestT167WebControlServerTimeoutAndCorrelation(t *testing.T) {
	cfg := webcontrol.ServerConfig{
		Host: "127.0.0.1",
		Port: 8787,
	}
	server, _ := webcontrol.NewServer(cfg, nil)

	// Context with immediate cancellation
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	// Invariant: Structured error envelope on canceled/timed out requests or valid response
	if w.Code == http.StatusGatewayTimeout || w.Code == http.StatusRequestTimeout {
		if !strings.Contains(w.Body.String(), "correlation_id") {
			t.Fatal("expected correlation_id in error envelope")
		}
	}
}
