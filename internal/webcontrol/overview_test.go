package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT181OverviewEndpoint(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", w.Code)
	}

	var overview webcontrol.OverviewSummaryDTO
	if err := json.NewDecoder(w.Body).Decode(&overview); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// Verify required sections exist
	if overview.SystemStatus.State != "READY" {
		t.Fatalf("expected system status READY, got: %s", overview.SystemStatus.State)
	}
	if len(overview.Providers) != 4 {
		t.Fatalf("expected 4 probed providers, got: %d", len(overview.Providers))
	}
	if overview.MemoryHealth == "" {
		t.Fatal("expected non-empty memory health indicator")
	}
}
