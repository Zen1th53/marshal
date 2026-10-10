package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestMarshalStatusLoadsStoredRun(t *testing.T) {
	fakeProviderCLIs(t, "codex", "claude", "opencode", "agy")
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

func TestMarshalStatusNonOwnerRefreshesExistingPanel(t *testing.T) {
	fakeProviderCLIs(t, "codex", "claude", "opencode", "agy")
	_, owner, ctx := acceptanceWorkspace(t)
	service := owner.runtime.Marshal()
	run := marshal.Run{PlanID: "owner-plan", PlanVersion: 1, Settings: marshal.DefaultSettings(), State: marshal.Dispatching,
		Tasks: []marshal.Task{{PlanTaskID: "live-task", Worker: "codex", State: marshal.Dispatched}}}
	revision, err := service.Store.SetMarshalRun(ctx, service.ProjectID, "owner-run", run, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.Open(ctx, owner.runtime.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	observer := NewWorkspace(second.Store(), second.ProjectID(), "observer")
	defer observer.Close()
	observer.AttachRuntime(second, owner.projectIdentity)
	observer.setMarshalPanel(newMarshalPanel("owner-run", "codex", run, "stale dispatch"))
	for _, state := range []struct {
		run  marshal.RunState
		task marshal.TaskState
	}{{marshal.Reviewing, marshal.HandedIn}, {marshal.Reviewing, marshal.Accepted}, {marshal.Closed, marshal.Merged}} {
		run.State, run.Tasks[0].State = state.run, state.task
		revision, err = service.Store.SetMarshalRun(ctx, service.ProjectID, "owner-run", run, revision)
		if err != nil {
			t.Fatal(err)
		}
		owner.setMarshalPanel(newMarshalPanel("owner-run", "codex", run, "owner updated"))
		for _, command := range []string{"/marshal status", "/marshal"} {
			text, err := NewCommandHandler(observer).Handle(ctx, command)
			if err != nil || !strings.Contains(text, "owner-run — "+string(state.run)) || !strings.Contains(text, string(state.task)+" · change") {
				t.Fatalf("%s stale after owner %s/%s: %q %v", command, state.run, state.task, text, err)
			}
			panel := observer.marshalPanel()
			if panel == nil || panel.State != owner.marshalPanel().State || panel.Tasks[0].State != owner.marshalPanel().Tasks[0].State {
				t.Fatalf("observer panel not refreshed: %+v", panel)
			}
		}
		stored, err := service.Store.GetMarshalRun(ctx, service.ProjectID, "owner-run")
		if err != nil || stored.Revision != revision {
			t.Fatalf("status changed stored revision: %+v %v", stored, err)
		}
	}
}
