package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT207MemoryUsageTraceAndReadReceipts(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	reqTrace := httptest.NewRequest(http.MethodGet, "/api/v1/memory/MEM-001-ARCH-DECISION/usage", nil)
	wTrace := httptest.NewRecorder()
	server.Handler().ServeHTTP(wTrace, reqTrace)

	if wTrace.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for usage trace, got: %d", wTrace.Code)
	}

	var resp webcontrol.MemoryUsageTraceResponseDTO
	_ = json.NewDecoder(wTrace.Body).Decode(&resp)

	if resp.TotalRecalls < 3 || resp.TotalInjections < 2 || resp.TotalCitations < 1 {
		t.Fatalf("unexpected usage counters: %+v", resp)
	}

	// Verify events distinguish retrieved vs injected vs cited
	typesFound := map[string]bool{}
	for _, ev := range resp.Events {
		typesFound[ev.EventType] = true
	}
	if !typesFound["retrieved"] || !typesFound["injected_to_prompt"] || !typesFound["cited_in_action"] {
		t.Fatalf("missing expected event types in usage trace: %+v", typesFound)
	}
}
