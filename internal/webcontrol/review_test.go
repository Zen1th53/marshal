package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT191ReviewCenterQueue(t *testing.T) {
	client := newAuthenticatedTestClient(t, "admin")

	// 1. List review queue
	reqQueue := httptest.NewRequest(http.MethodGet, "/api/v1/review/queue", nil)
	wQueue := client.Do(reqQueue)

	if wQueue.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", wQueue.Code)
	}

	var resp webcontrol.ReviewQueueResponseDTO
	_ = json.NewDecoder(wQueue.Body).Decode(&resp)

	if resp.TotalCount < 3 || len(resp.Items) < 3 {
		t.Fatalf("expected at least 3 review queue items, got: %d", resp.TotalCount)
	}

	// 2. Filter by stage=merge_approval
	reqMerge := httptest.NewRequest(http.MethodGet, "/api/v1/review/queue?stage=merge_approval", nil)
	wMerge := client.Do(reqMerge)

	var respMerge webcontrol.ReviewQueueResponseDTO
	_ = json.NewDecoder(wMerge.Body).Decode(&respMerge)

	if len(respMerge.Items) != 1 || respMerge.Items[0].Stage != "merge_approval" {
		t.Fatalf("expected 1 merge approval item, got: %d", len(respMerge.Items))
	}

	// 3. Stale head item detection
	foundStale := false
	for _, item := range resp.Items {
		if item.IsStaleHead {
			foundStale = true
			if item.TaskID != "TASK-004-BENCHMARKS" {
				t.Fatalf("unexpected stale item: %s", item.TaskID)
			}
		}
	}
	if !foundStale {
		t.Fatal("expected to find at least one stale head review item")
	}
}
