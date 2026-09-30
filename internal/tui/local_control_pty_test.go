//go:build linux

package tui

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// The shipped terminal has no released mutation entry in stage 1. Exercise the
// real in-process workspace adapter, then read back using the unmodified real
// binary over a PTY. This does not claim terminal-originated mutation coverage.
func TestPTYLocalControlProofReadbackAndSlashRefusal(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)                     // isolated HOME/XDG/provider config, project and go/git PATH
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1") // also disable fixed provider search directories
	project := initProject(t, bin)
	t.Chdir(project)
	ctx := context.Background()
	runtime, err := app.Open(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	projectID := runtime.ProjectIdentity()
	if !projectid.ID(projectID).Valid() {
		t.Fatal("project has no canonical binding")
	}
	const session = "SESSION-pty-local-control"
	goal := model.GoalContract{ID: "GOAL-pty-local-control", SessionID: session, ProjectID: projectID, Revision: 1, OriginalRequest: "fix README typo", DesiredOutcome: "fix README typo", ConstitutionVersion: constitution.Current.String(), Confirmation: model.ConfirmationApproved, Risk: model.R1, AuthoritySource: "owner", SuccessCriteria: []string{"typo fixed"}}
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	workspace := NewWorkspace(runtime.Store(), projectID, session)
	workspace.AttachRuntime(runtime, projectid.ID(projectID))
	request := ActionRequest{Target: Target{Kind: "goal", ID: goal.ID, Revision: 1}, IdempotencyKey: "pty-local-proof", Inputs: map[string]string{"interpretation": "PTY canonical local proof wording", "reason": "owner clarified wording"}}
	outcome, err := workspace.controlSource().executeReviseGoal(ctx, request)
	if err != nil || outcome.Verdict != VerdictPass {
		t.Fatalf("workspace proof mutation: %+v %v", outcome, err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	terminal := startFrozenTUIInProject(t, 40, 180, bin, project, "tui", session)
	terminal.mustSee("MARSHAL")
	terminal.sendLine("/goal")
	terminal.mustSee("Active Goal [v2]: PTY canonical local proof wording")
	terminal.sendLine("/goal forged worker interpretation")
	terminal.mustSee("Goal mutation is unavailable in TUI")
	terminal.sendLine("/goal")
	terminal.mustSee("Active Goal [v2]: PTY canonical local proof wording")
	reopened, err := app.Open(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, err := reopened.Store().GetActiveGoalContract(ctx, session)
	if err != nil || stored.Revision != 2 || stored.DesiredOutcome != "PTY canonical local proof wording" || stored.Confirmation != model.ConfirmationPending {
		t.Fatalf("terminal readback: %+v %v", stored, err)
	}
}
