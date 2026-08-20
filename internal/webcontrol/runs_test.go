package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT188RunsExplorer(t *testing.T) {
	client := newAuthenticatedTestClient(t, "admin")

	// 1. List all runs
	reqAll := httptest.NewRequest(http.MethodGet, "/api/v1/runs?limit=10", nil)
	wAll := client.Do(reqAll)

	if wAll.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", wAll.Code)
	}

	var resp webcontrol.RunsListResponseDTO
	_ = json.NewDecoder(wAll.Body).Decode(&resp)

	if resp.TotalCount < 3 || len(resp.Items) < 3 {
		t.Fatalf("expected at least 3 runs, got total: %d, items: %d", resp.TotalCount, len(resp.Items))
	}

	// 2. Filter by status=running
	reqRunning := httptest.NewRequest(http.MethodGet, "/api/v1/runs?status=running", nil)
	wRunning := client.Do(reqRunning)

	var respRunning webcontrol.RunsListResponseDTO
	_ = json.NewDecoder(wRunning.Body).Decode(&respRunning)

	if len(respRunning.Items) != 1 || respRunning.Items[0].Status != "running" {
		t.Fatalf("expected 1 running run, got: %d", len(respRunning.Items))
	}

	// 3. Filter by task_id
	reqTask := httptest.NewRequest(http.MethodGet, "/api/v1/runs?task_id=TASK-001-CORE-MEMORY", nil)
	wTask := client.Do(reqTask)

	var respTask webcontrol.RunsListResponseDTO
	_ = json.NewDecoder(wTask.Body).Decode(&respTask)

	if len(respTask.Items) != 1 || respTask.Items[0].TaskID != "TASK-001-CORE-MEMORY" {
		t.Fatalf("expected 1 run for TASK-001, got: %d", len(respTask.Items))
	}
}
