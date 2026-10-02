package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestMarshalResumeRecoversStoredRun(t *testing.T) {
	store, first, ctx := acceptanceWorkspace(t)
	projectID := first.projectID
	if _, err := store.SetMarshalRun(ctx, projectID, "RUN-recover", marshal.Run{PlanID: "PLAN-recover", PlanVersion: 1, Settings: marshal.DefaultSettings(), State: marshal.AwaitingUser}, 0); err != nil {
		t.Fatal(err)
	}
	second := NewWorkspace(store, projectID, "fresh-session")
	second.AttachRuntime(first.runtime, first.projectIdentity)
	out, err := second.ExecuteCommand(ctx, "/marshal resume")
	if err != nil || !strings.Contains(out, "RUN-recover") {
		t.Fatalf("resume: %q %v", out, err)
	}
	if second.marshalSession().runID != "RUN-recover" {
		t.Fatal("resume did not bind the stored run")
	}
}

func TestMarshalComposerBudgetSettings(t *testing.T) {
	_, ws, ctx := acceptanceWorkspace(t)
	for _, key := range []string{"task-tokens", "plan-tokens", "task-money", "plan-money", "task-wall-seconds", "plan-wall-seconds"} {
		if _, err := ws.ExecuteCommand(ctx, "/marshal settings "+key+" 12"); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
	}
	out, err := ws.ExecuteCommand(ctx, "/marshal settings")
	if err != nil || !strings.Contains(out, "plan-tokens 12") || !strings.Contains(out, "task-wall-seconds 12") {
		t.Fatalf("settings: %s %v", out, err)
	}
	if _, err := ws.ExecuteCommand(ctx, "/marshal settings plan-tokens -1"); err == nil {
		t.Fatal("negative ceiling stored")
	}
}

func TestHelpDescribesApprovalHandlers(t *testing.T) {
	_, ws, ctx := acceptanceWorkspace(t)
	out, err := ws.ExecuteCommand(ctx, "/help")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out, "\n") {
		if (strings.Contains(line, "/approve ") || strings.Contains(line, "/reject ")) && strings.Contains(line, "Unavailable") {
			t.Fatal(line)
		}
	}
}
