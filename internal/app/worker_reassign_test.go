package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// run10: opencode's free quota was spent, its verification failed, and the
// Marshal asked the owner to re-verify for ten minutes. Its tasks now move to
// the next verified provider in codex > claude > agy > opencode order.
func TestReassignDraftWorkersMovesUnverifiedProviderInOrder(t *testing.T) {
	draft := MarshalDraft{Tasks: []marshal.Task{
		{PlanTaskID: "fix", Worker: "claude", Mode: "governed"},
		{PlanTaskID: "readme", Worker: "opencode", Mode: "governed"},
		{PlanTaskID: "outside", Worker: "OpenCode", Mode: "governed"},
		{PlanTaskID: "native", Worker: "opencode", Mode: "native"},
	}}
	verified := map[string]bool{"claude": true, "agy": true}
	var moves []string
	err := reassignDraftWorkers(&draft, func(p string) bool { return verified[p] }, func(task, from, to string) error {
		moves = append(moves, task+":"+from+">"+to)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(moves, ","); got != "readme:opencode>claude,outside:opencode>claude" {
		t.Fatalf("moves = %s", got)
	}
	if draft.Tasks[0].Worker != "claude" || draft.Tasks[1].Worker != "claude" || draft.Tasks[2].Worker != "claude" || draft.Tasks[3].Worker != "opencode" {
		t.Fatalf("workers = %+v", draft.Tasks)
	}
}

func TestReassignDraftWorkersKeepsWorkerWithoutAlternativeAndRecordsFirst(t *testing.T) {
	draft := MarshalDraft{Tasks: []marshal.Task{{PlanTaskID: "t", Worker: "opencode", Mode: "governed"}}}
	if err := reassignDraftWorkers(&draft, func(string) bool { return false }, func(string, string, string) error { return nil }); err != nil || draft.Tasks[0].Worker != "opencode" {
		t.Fatalf("no alternative: %v %+v", err, draft.Tasks)
	}
	failed := errors.New("evidence unavailable")
	err := reassignDraftWorkers(&draft, func(p string) bool { return p == "codex" }, func(string, string, string) error { return failed })
	if !errors.Is(err, failed) || draft.Tasks[0].Worker != "opencode" {
		t.Fatalf("unrecorded move applied: %v %+v", err, draft.Tasks)
	}
}

func TestProviderLimitReasonNamesTheQuotaMessage(t *testing.T) {
	stdout := []byte(`{"type":"step_start"}` + "\n" + `{"type":"error","message":"You've used today's free messages. Try again after 2026-10-30 05:03."}` + "\n")
	if got := providerLimitReason(stdout, []byte("socat[4] W exiting on signal 15")); !strings.Contains(got, "free messages") {
		t.Fatalf("limit reason = %q", got)
	}
	if got := providerLimitReason([]byte("MARSHAL_PROBE_OK"), []byte("socat[4] W exiting on signal 15")); got != "" {
		t.Fatalf("ordinary output reported as a limit: %q", got)
	}
}
