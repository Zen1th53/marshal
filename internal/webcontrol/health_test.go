package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT209SystemHealthAndDoctorReport(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	reqDoctor := httptest.NewRequest(http.MethodGet, "/api/v1/health/doctor", nil)
	wDoctor := httptest.NewRecorder()
	server.Handler().ServeHTTP(wDoctor, reqDoctor)

	if wDoctor.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for doctor report, got: %d", wDoctor.Code)
	}

	var resp webcontrol.DoctorReportDTO
	_ = json.NewDecoder(wDoctor.Body).Decode(&resp)

	if resp.OverallStatus != "READY" || len(resp.Checks) < 7 {
		t.Fatalf("unexpected doctor report: %+v", resp)
	}

	// Verify required core components checked
	foundComps := map[string]bool{}
	for _, c := range resp.Checks {
		foundComps[c.Component] = true
		if c.Status != "READY" {
			t.Errorf("expected component %s to be READY, got: %s", c.Component, c.Status)
		}
	}

	required := []string{
		"database_sqlite",
		"event_bus",
		"worker_fleet",
		"providers",
		"memory_indexes",
		"sandbox_isolation",
		"version_integrity",
	}
	for _, req := range required {
		if !foundComps[req] {
			t.Errorf("missing required diagnostic component check: %s", req)
		}
	}
}
