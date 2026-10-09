package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestMarshalStatusLoadsStoredRun(t *testing.T) {
	_, w, ctx := acceptanceWorkspace(t)
	if text, err := NewCommandHandler(w).Handle(ctx, "/marshal status"); err != nil || !strings.Contains(text, "No Marshal run") {
		t.Fatalf("empty project status: %q %v", text, err)
	}
	run := marshal.Run{PlanID: "owner-plan", PlanVersion: 1, Settings: marshal.DefaultSettings(), State: marshal.AwaitingUser, Tasks: []marshal.Task{{PlanTaskID: "stored-task", State: marshal.Escalated}}}
	if _, err := w.runtime.Store().SetMarshalRun(ctx, w.runtime.Marshal().ProjectID, "stored-run", run, 0); err != nil {
		t.Fatal(err)
	}
	second, err := app.Open(ctx, w.runtime.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	w.AttachRuntime(second, w.projectIdentity)
	if w.marshalPanel() != nil {
		t.Fatal("expected empty panel")
	}
	for _, command := range []string{"/marshal status", "/marshal"} {
		text, err := NewCommandHandler(w).Handle(ctx, command)
		if err != nil || !strings.Contains(text, "stored-run") || !strings.Contains(text, "stored-task") || !strings.Contains(text, "escalated") {
			t.Fatalf("%s failed to load stored status: %q %v", command, text, err)
		}
	}
	record, err := w.runtime.Store().GetMarshalRun(ctx, w.runtime.Marshal().ProjectID, "stored-run")
	if err != nil || record.Revision != 1 {
		t.Fatalf("status mutated stored run: %+v %v", record, err)
	}
}
