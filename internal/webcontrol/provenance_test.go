package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT195ProvenanceWhyTrace(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. Get Provenance Trace
	reqTrace := httptest.NewRequest(http.MethodGet, "/api/v1/provenance/trace?target_id=TASK-002-CONTROL-PLANE&depth=5", nil)
	wTrace := httptest.NewRecorder()
	server.Handler().ServeHTTP(wTrace, reqTrace)

	if wTrace.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", wTrace.Code)
	}

	var traceResp webcontrol.ProvenanceTraceResponseDTO
	_ = json.NewDecoder(wTrace.Body).Decode(&traceResp)

	if traceResp.TargetID != "TASK-002-CONTROL-PLANE" || len(traceResp.Nodes) < 4 {
		t.Fatalf("unexpected provenance trace response: %+v", traceResp)
	}

	// 2. Verify distinction between cryptographic binding and correlation
	hasBinding := false
	hasCorrelation := false
	for _, n := range traceResp.Nodes {
		if n.Type == "memory_injection" && n.IsProvenBinding {
			hasBinding = true
		}
		if n.Type == "audit_event" && !n.IsProvenBinding {
			hasCorrelation = true
		}
	}

	if !hasBinding || !hasCorrelation {
		t.Fatalf("expected both proven bindings and correlation links: %+v", traceResp.Nodes)
	}

	// 3. Depth bounding test
	reqDeep := httptest.NewRequest(http.MethodGet, "/api/v1/provenance/trace?depth=99", nil)
	wDeep := httptest.NewRecorder()
	server.Handler().ServeHTTP(wDeep, reqDeep)

	var deepResp webcontrol.ProvenanceTraceResponseDTO
	_ = json.NewDecoder(wDeep.Body).Decode(&deepResp)

	if deepResp.MaxDepth > 10 {
		t.Fatalf("expected depth to be capped at 10, got: %d", deepResp.MaxDepth)
	}
}
