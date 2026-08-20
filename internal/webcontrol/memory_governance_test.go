package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT203MemoryGovernanceQueueAndConflicts(t *testing.T) {
	client := newAuthenticatedTestClient(t, "admin")

	// 1. List all governance queue items
	reqList := httptest.NewRequest(http.MethodGet, "/api/v1/memory/governance/queue", nil)
	wList := client.Do(reqList)

	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", wList.Code)
	}

	var listResp webcontrol.GovernanceQueueResponseDTO
	_ = json.NewDecoder(wList.Body).Decode(&listResp)

	if listResp.TotalCount < 4 {
		t.Fatalf("expected at least 4 governance items, got: %d", listResp.TotalCount)
	}

	// 2. Filter by category=conflicted
	reqConf := httptest.NewRequest(http.MethodGet, "/api/v1/memory/governance/queue?category=conflicted", nil)
	wConf := client.Do(reqConf)

	var confResp webcontrol.GovernanceQueueResponseDTO
	_ = json.NewDecoder(wConf.Body).Decode(&confResp)

	if len(confResp.Items) != 1 || confResp.Items[0].Category != "conflicted" {
		t.Fatalf("expected 1 conflicted item, got: %+v", confResp)
	}

	// 3. Get Conflict Comparison Details
	reqComp := httptest.NewRequest(http.MethodGet, "/api/v1/memory/governance/conflicts/GOV-CONF-001", nil)
	wComp := client.Do(reqComp)

	if wComp.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for conflict comparison, got: %d", wComp.Code)
	}

	var compResp webcontrol.MemoryConflictComparisonDTO
	_ = json.NewDecoder(wComp.Body).Decode(&compResp)

	if compResp.BaseMemory.ID == "" || compResp.CompetingMemory.ID == "" || compResp.ResolutionMode != "manual_review_required" {
		t.Fatalf("invalid conflict comparison payload: %+v", compResp)
	}
}
