//go:build linux

package tui

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// This acceptance test uses the actual candidate binary. Integration must
// provide periodic/action-bound checkpoints and source-process liveness; a
// service fixture seeded in the test cannot substitute for crash recovery.
func TestPTYInterruptedWorkspaceRecovery(t *testing.T) {
	if !editionExtreme {
		t.Skip("candidate navigation qualification requires (default build)")
	}
	openPTY(t)
	if store.LatestSchemaVersion < 94 {
		t.Skip("integration prerequisite: register memory v93 then recovery v94")
	}
	if _, ok := any(&app.Runtime{}).(sessionRecoveryHookProvider); !ok {
		t.Skip("integration prerequisite: trusted recovery hooks/startup/checkpoint/adoption wiring")
	}
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	t.Setenv("MARSHAL_CLOUD_ENDPOINT", "off")
	project := initProject(t, bin)
	const source = "SESSION-interrupted-workspace"
	const destination = "SESSION-recovered-workspace"
	first := startFrozenTUIInProject(t, 40, 160, bin, project, "tui", source)
	first.sendLine("/goal create Fix the scratch parser typo. Do not change the public API.")
	first.mustSee("[rev 1]")
	var saved model.SessionRecovery
	deadline := time.Now().Add(ptyWait(15 * time.Second))
	for time.Now().Before(deadline) {
		r, err := app.Open(context.Background(), project)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := r.WorkspaceRecovery(app.SessionRecoveryHooks{}).List(context.Background())
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.SourceSessionID == source {
				saved = row
				break
			}
		}
		if saved.ID != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if saved.ID == "" {
		t.Fatal("live scratch TUI did not persist a recoverable context")
	}
	if err := first.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = first.cmd.Wait()
	// Reread after SIGKILL, so any checkpoint just before death is selected
	// exactly rather than substituting a guessed generation in confirmation.
	r, err := app.Open(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	saved, err = r.WorkspaceRecovery(app.SessionRecoveryHooks{}).Get(context.Background(), saved.ID, 0)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	second := startFrozenTUIInProject(t, 40, 160, bin, project, "tui", destination)
	second.send("\x0e")
	recoveryMenuPTYAction(second, "Resume interrupted session")
	second.mustSee("Exact durable workspace recovery ID")
	second.send(saved.ID + "\r")
	second.mustSee("Proceed")
	second.send("\x1b[C\r")
	second.mustSee("Workspace references adopted")
	r, err = app.Open(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	proof, err := r.WorkspaceRecovery(app.SessionRecoveryHooks{}).Get(context.Background(), saved.ID, 0)
	if err != nil || proof.State != "adopted" || proof.AdoptedSessionID != destination || proof.Binding != saved.Binding {
		t.Fatalf("durable crash adoption: %+v %v", proof, err)
	}
	goal, err := r.Store().GetGoalContract(context.Background(), saved.Binding.GoalID, saved.Binding.GoalRevision)
	if err != nil || goal.Confirmation != model.ConfirmationPending {
		t.Fatalf("adoption replayed confirmation: %+v %v", goal, err)
	}
	runs, err := r.Store().SessionInventoryRuns(context.Background(), r.ProjectIdentity(), "")
	if err != nil || len(runs) != 0 {
		t.Fatalf("adoption dispatched a provider: %d %v", len(runs), err)
	}
}

func recoveryMenuPTYAction(s *ptySession, title string) {
	s.send("\x0b")
	s.send(title)
	s.send("\r")
	s.send("\r")
}
