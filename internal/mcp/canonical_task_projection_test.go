package mcp

import (
	"strings"
	"testing"
)

// Handed-in, reviewed-but-unmerged and pending-verification work is never
// summarised as complete or done: only merged work counts as done.
func TestCanonicalTaskProjectionsNeverCallPendingComplete(t *testing.T) {
	pending := `[{"id":"a","state":"handed_in","attempt":1},{"id":"b","state":"accepted","attempt":2},{"id":"c","state":"dispatched","attempt":1}]`
	for _, tc := range []struct{ name, text, want string }{
		{"run_digest", `{"run_id":"RUN-1","state":"reviewing","tasks":` + pending + `}`, "The run is reviewing: 0 of 3 tasks done."},
		{"marshal_status", `{"state":"reviewing","tasks":` + pending + `}`, "Status: run reviewing, 0 of 3 tasks done."},
		{"run_digest", `{"run_id":"RUN-1","state":"reviewing","tasks":[{"id":"a","state":"merged"},{"id":"b","state":"accepted"}]}`, "The run is reviewing: 1 of 2 tasks done."},
		{"verification_status", `{"state":"pending"}`, "Verification has no result yet."},
	} {
		if got := toolResultSummary(tc.name, tc.text); got != tc.want {
			t.Fatalf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	for _, status := range []string{"working", "review", "qa", "security_review", "ready_to_merge", "blocked"} {
		got := toolResultSummary("task_get", `{"id":"TASK-1","title":"Add square","status":"`+status+`"}`)
		if low := strings.ToLower(got); strings.Contains(low, "complete") || strings.Contains(low, "done") {
			t.Fatalf("task_get %s: %q", status, got)
		}
	}
	if got := toolResultSummary("tasks_list", `[{"id":"TASK-1","status":"review"}]`); strings.Contains(strings.ToLower(got), "complete") {
		t.Fatalf("tasks_list: %q", got)
	}
}
