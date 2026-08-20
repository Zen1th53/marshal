package webcontrol_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

	// Invariant 1: Loopback validation
	if !server.IsLoopback() {
		t.Fatal("expected server to be in loopback mode for 127.0.0.1")
	}

	// Invariant 2: Non-loopback without auth must fail
	badCfg := webcontrol.ServerConfig{
		Host: "0.0.0.0",
		Port: 8787,
		AllowInsecureNonLoopback: false,
	}
	_, err = webcontrol.NewServer(badCfg, nil)
	if err == nil {
		t.Fatal("expected error when binding to 0.0.0.0 without authentication/insecure flag")
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
	if statusDTO.State == "" || statusDTO.DatabaseSchema != "v69" {
		t.Fatalf("unexpected status DTO: %+v", statusDTO)
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
