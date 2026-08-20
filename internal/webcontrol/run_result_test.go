package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT190RunResultsArtifactsAndRecovery(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. Get Run Result
	reqResult := httptest.NewRequest(http.MethodGet, "/api/v1/runs/RUN-001/result", nil)
	wResult := httptest.NewRecorder()
	server.Handler().ServeHTTP(wResult, reqResult)

	if wResult.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for result, got %d", wResult.Code)
	}

	var res webcontrol.RunResultComprehensiveDTO
	_ = json.NewDecoder(wResult.Body).Decode(&res)

	if len(res.FilesSummary) < 2 || len(res.Artifacts) < 2 {
		t.Fatalf("expected diff summary and artifacts in result, got: %+v", res)
	}

	// 2. Safe Artifact Download
	reqDownload := httptest.NewRequest(http.MethodGet, "/api/v1/artifacts/art-001/download", nil)
	wDownload := httptest.NewRecorder()
	server.Handler().ServeHTTP(wDownload, reqDownload)

	if wDownload.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for artifact download, got %d", wDownload.Code)
	}

	h := wDownload.Header()
	if !strings.Contains(h.Get("Content-Disposition"), "attachment; filename=") {
		t.Fatalf("expected attachment Content-Disposition, got: %s", h.Get("Content-Disposition"))
	}
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("expected X-Content-Type-Options: nosniff, got: %s", h.Get("X-Content-Type-Options"))
	}

	// 3. Security Invariant: Path Traversal IDOR rejection
	reqEvil := httptest.NewRequest(http.MethodGet, "/api/v1/artifacts/..%2F..%2Fetc%2Fpasswd/download", nil)
	wEvil := httptest.NewRecorder()
	server.Handler().ServeHTTP(wEvil, reqEvil)

	if wEvil.Code != http.StatusBadRequest && wEvil.Code != http.StatusNotFound {
		t.Fatalf("expected 400 or 404 for traversal attempt, got %d", wEvil.Code)
	}
}
