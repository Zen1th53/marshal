package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT185TaskComprehensiveDetail(t *testing.T) {
	client := newAuthenticatedTestClient(t, "admin")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/TASK-001-CORE-MEMORY", nil)
	w := client.Do(req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", w.Code)
	}

	var detail webcontrol.TaskComprehensiveDetailDTO
	if err := json.NewDecoder(w.Body).Decode(&detail); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if len(detail.LifecycleHistory) != 3 {
		t.Fatalf("expected 3 lifecycle events, got %d", len(detail.LifecycleHistory))
	}
	if detail.CorrelationID == "" || !strings.HasPrefix(detail.CorrelationID, "req-audit-") {
		t.Fatalf("expected valid audit correlation ID, got: %s", detail.CorrelationID)
	}
	if len(detail.Runs) != 1 || detail.Runs[0].StepCount != 12 {
		t.Fatalf("unexpected runs summary: %+v", detail.Runs)
	}
}
