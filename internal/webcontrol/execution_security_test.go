package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT198ExecutionBoundaryAndSandboxEnforcement(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. Get Boundary for standard run
	reqBoundary := httptest.NewRequest(http.MethodGet, "/api/v1/runs/RUN-001/boundary", nil)
	wBoundary := httptest.NewRecorder()
	server.Handler().ServeHTTP(wBoundary, reqBoundary)

	if wBoundary.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", wBoundary.Code)
	}

	var boundary webcontrol.ExecutionBoundaryDTO
	_ = json.NewDecoder(wBoundary.Body).Decode(&boundary)

	if boundary.SandboxBackend != "bubblewrap" || !boundary.IsNetworkIsolated || boundary.Memory.Limit <= 0 {
		t.Fatalf("unexpected execution boundary data: %+v", boundary)
	}

	// 2. Sensitive Mount Redaction Scan
	for _, m := range boundary.MountedPaths {
		if strings.Contains(m, "/etc/shadow") || strings.Contains(m, ".ssh") || strings.Contains(m, "/root") {
			t.Fatalf("CRITICAL SECURITY DEFECT: found sensitive path in mounts: %s", m)
		}
	}

	// 3. OOM Killed diagnostic test
	reqOOM := httptest.NewRequest(http.MethodGet, "/api/v1/runs/RUN-TASK-OOM-01/boundary", nil)
	wOOM := httptest.NewRecorder()
	server.Handler().ServeHTTP(wOOM, reqOOM)

	var oomResp webcontrol.ExecutionBoundaryDTO
	_ = json.NewDecoder(wOOM.Body).Decode(&oomResp)

	if !oomResp.WasOOMKilled || oomResp.Memory.UsagePct < 100.0 {
		t.Fatalf("expected OOM killed diagnostic, got: %+v", oomResp)
	}
}
