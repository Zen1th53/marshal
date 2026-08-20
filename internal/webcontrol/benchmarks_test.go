package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT212BenchmarksAndConformanceDashboard(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/benchmarks", nil)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for list benchmarks, got: %d", w.Code)
	}

	var resp webcontrol.BenchmarksResponseDTO
	_ = json.NewDecoder(w.Body).Decode(&resp)

	if resp.TotalSuites < 4 || len(resp.Reports) < 4 {
		t.Fatalf("unexpected benchmarks count: %+v", resp)
	}

	hasNotRun := false
	hasInternalCompatible := false
	hasOfficialFull := false

	for _, rep := range resp.Reports {
		if rep.Status == "NOT_RUN" {
			hasNotRun = true
		}
		if rep.HarnessType == "internal_compatible" {
			hasInternalCompatible = true
		}
		if rep.HarnessType == "official_full" {
			hasOfficialFull = true
		}
		if rep.ScopeNotice == "" {
			t.Errorf("benchmark suite %s missing honest scope notice", rep.SuiteID)
		}
	}

	if !hasNotRun {
		t.Errorf("expected at least one suite flagged NOT_RUN for full harness honesty")
	}
	if !hasInternalCompatible || !hasOfficialFull {
		t.Errorf("expected both internal_compatible and official_full harnesses in report")
	}
}
