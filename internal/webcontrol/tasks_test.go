package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT183TasksListAndDetail(t *testing.T) {
	client := newAuthenticatedTestClient(t, "admin")

	// 1. List tasks with status filter
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?status=running", nil)
	w := client.Do(req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", w.Code)
	}

	var paged webcontrol.PagedResponse[webcontrol.TaskSummaryDTO]
	if err := json.NewDecoder(w.Body).Decode(&paged); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if len(paged.Items) != 1 || paged.Items[0].Status != webcontrol.TaskStatusRunning {
		t.Fatalf("expected 1 running task, got: %+v", paged.Items)
	}

	// 2. Search tasks by title / keyword
	reqSearch := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?search=memory", nil)
	wSearch := client.Do(reqSearch)

	var pagedSearch webcontrol.PagedResponse[webcontrol.TaskSummaryDTO]
	if err := json.NewDecoder(wSearch.Body).Decode(&pagedSearch); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(pagedSearch.Items) == 0 {
		t.Fatal("expected at least 1 memory task match")
	}

	// 3. Detail view
	reqDetail := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/TASK-001-CORE-MEMORY", nil)
	wDetail := client.Do(reqDetail)
	if wDetail.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for task detail, got: %d", wDetail.Code)
	}

	var detail webcontrol.TaskDetailDTO
	if err := json.NewDecoder(wDetail.Body).Decode(&detail); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if detail.ID != "TASK-001-CORE-MEMORY" || detail.Risk != "HIGH" {
		t.Fatalf("unexpected detail: %+v", detail)
	}

	// 4. Unknown task ID
	req404 := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/TASK-UNKNOWN-999", nil)
	w404 := client.Do(req404)
	if w404.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown task, got %d", w404.Code)
	}

	// 5. Secret leak check
	bodyStr := wDetail.Body.String()
	for _, forbidden := range []string{"password", "private_key", "bearer_token"} {
		if strings.Contains(strings.ToLower(bodyStr), forbidden) {
			t.Fatalf("forbidden secret keyword %q found in task payload", forbidden)
		}
	}
}
