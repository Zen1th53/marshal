package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT201MemoryDetailProvenanceAndLifecycle(t *testing.T) {
	client := newAuthenticatedTestClient(t, "admin")

	// 1. Get Memory Detail for MEM-001
	reqDetail := httptest.NewRequest(http.MethodGet, "/api/v1/memory/MEM-001-ARCH-DECISION/detail", nil)
	wDetail := client.Do(reqDetail)

	if wDetail.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", wDetail.Code)
	}

	var detail webcontrol.MemoryDetailDTO
	_ = json.NewDecoder(wDetail.Body).Decode(&detail)

	if len(detail.DigestSHA256) != 64 || detail.Revision <= 0 || detail.Provenance.ProducerAgentID == "" {
		t.Fatalf("unexpected memory detail response: %+v", detail)
	}

	// 2. Temporal Memory Record (Belief MEM-004 has expiration)
	reqBelief := httptest.NewRequest(http.MethodGet, "/api/v1/memory/MEM-004-CANDIDATE-HEURISTIC/detail", nil)
	wBelief := client.Do(reqBelief)

	var beliefDetail webcontrol.MemoryDetailDTO
	_ = json.NewDecoder(wBelief.Body).Decode(&beliefDetail)

	if beliefDetail.ExpiresAt == nil {
		t.Fatal("expected non-nil ExpiresAt for temporal belief record")
	}

	// 3. 404 for Unknown ID
	req404 := httptest.NewRequest(http.MethodGet, "/api/v1/memory/MEM-UNKNOWN-999/detail", nil)
	w404 := client.Do(req404)

	if w404.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got: %d", w404.Code)
	}
}
