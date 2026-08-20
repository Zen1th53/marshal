package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT189RunDetailAndSafeLogs(t *testing.T) {
	client := newAuthenticatedTestClient(t, "admin")

	// 1. Get run detail
	reqDetail := httptest.NewRequest(http.MethodGet, "/api/v1/runs/RUN-TASK-001-01", nil)
	wDetail := client.Do(reqDetail)

	if wDetail.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for run detail, got: %d", wDetail.Code)
	}

	var detail webcontrol.RunDetailComprehensiveDTO
	_ = json.NewDecoder(wDetail.Body).Decode(&detail)

	if detail.RunID != "RUN-TASK-001-01" || detail.TaskID != "TASK-001-CORE-MEMORY" {
		t.Fatalf("unexpected run detail data: %+v", detail)
	}
	if len(detail.Logs) < 5 {
		t.Fatalf("expected at least 5 log lines in detail, got %d", len(detail.Logs))
	}

	// 2. Query paginated logs with cursor
	reqLogs := httptest.NewRequest(http.MethodGet, "/api/v1/runs/RUN-TASK-001-01/logs?cursor=0&limit=3", nil)
	wLogs := client.Do(reqLogs)

	if wLogs.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for logs, got: %d", wLogs.Code)
	}

	var logsResp webcontrol.RunLogsResponseDTO
	_ = json.NewDecoder(wLogs.Body).Decode(&logsResp)

	if len(logsResp.Lines) != 3 {
		t.Fatalf("expected 3 lines for limit=3, got: %d", len(logsResp.Lines))
	}

	// 3. Security invariant: zero raw ANSI escape codes in messages
	for _, l := range logsResp.Lines {
		if strings.Contains(l.Message, "\x1b[") {
			t.Fatalf("ANSI escape sequence detected in sanitized log line: %q", l.Message)
		}
	}
}
