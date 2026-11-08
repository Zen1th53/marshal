package inspector

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

func TestInspectorCodeEvidenceSection(t *testing.T) {
	b := model.Board{Tasks: []model.TaskRow{{ID: "a", Title: "ledger", State: model.TaskReview}}}
	plain := strings.Join(New(b, "a").Render(120, 60), "\n")
	if strings.Contains(plain, "code evidence") {
		t.Fatalf("section shown without receipts:\n%s", plain)
	}
	b.Events = []events.Event{
		{ID: "e1", Type: events.EventTypeMarshalEvidenceReceipt, TaskID: "a", Data: map[string]any{"receipt": "rcpt_x"}},
		{ID: "e0", Type: events.EventTypeMarshalEvidenceReceipt, TaskID: "other", Data: map[string]any{"receipt": "rcpt_y"}},
	}
	if got := strings.Join(New(b, "a").Render(120, 60), "\n"); !strings.Contains(got, "code evidence: 1 receipts · not validated yet") {
		t.Fatalf("pending:\n%s", got)
	}
	b.Events = append(b.Events, events.Event{ID: "e2", Type: events.EventTypeMarshalEvidenceValidated, TaskID: "a", Data: map[string]any{
		"phase": "hand-in", "policy": "advisory", "holds": float64(5), "invalidated": float64(2), "unknown": float64(1), "own": float64(1), "paths": []any{"ledger/ledger.go"},
	}})
	got := strings.Join(New(b, "a").Render(120, 60), "\n")
	for _, want := range []string{"EVIDENCE", "code evidence (hand-in, advisory): 5 holds · 2 invalidated · 1 unknown", "1 changed by the task itself", "changed by other work: ledger/ledger.go"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "code evidence") > strings.Index(got, "TIMELINE") {
		t.Fatal("code evidence must sit in the EVIDENCE section")
	}
}
