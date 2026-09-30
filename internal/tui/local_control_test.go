package tui

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestWorkspaceGoalRevisionAuthenticatedReadback(t *testing.T) {
	ctx := context.Background()
	_, runtime := realControlWorkspace(t, "SESSION-local-control")
	project, err := runtime.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// A fresh workspace has no entitlement. Operator authorization is separate.
	workspace := NewWorkspace(runtime.Store(), project.ID, "SESSION-local-control")
	workspace.AttachRuntime(runtime, projectid.ID(project.ID))
	goal := model.GoalContract{ID: "GOAL-local-control", SessionID: "SESSION-local-control", ProjectID: project.ID, Revision: 1, OriginalRequest: "fix the typo", DesiredOutcome: "fix the typo", ConstitutionVersion: constitution.Current.String(), Confirmation: model.ConfirmationApproved, Risk: model.R1, AuthoritySource: "owner", SuccessCriteria: []string{"typo fixed"}, Constraints: []model.Constraint{{ID: "hard", Text: "keep the API", IsHard: true, Source: "owner"}}}
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	source := workspace.controlSource()
	request := ActionRequest{Target: Target{Kind: "goal", ID: goal.ID, Revision: 1}, IdempotencyKey: "workspace-proof", Inputs: map[string]string{"interpretation": "fix only the README typo", "reason": "scope clarification", "role": "operator"}}
	for i := 0; i < 2; i++ {
		outcome, err := source.executeReviseGoal(ctx, request)
		if err != nil || outcome.Verdict != VerdictPass || outcome.Target.Revision != 2 {
			t.Fatalf("submission %d: %+v %v", i, outcome, err)
		}
	}
	active, err := runtime.Store().GetActiveGoalContract(ctx, goal.SessionID)
	if err != nil || active.Revision != 2 || active.Confirmation != model.ConfirmationPending || active.DesiredOutcome != "fix only the README typo" || len(active.Constraints) != 1 {
		t.Fatalf("canonical readback: %+v %v", active, err)
	}
	// A runtime adapter created by a worker path has no workspace session.
	unbound := &runtimeControlAuthority{runtime: runtime, store: runtime.Store()}
	source.Authority = unbound
	request.IdempotencyKey = "worker-spoof"
	request.Target.Revision = 2
	outcome, err := source.executeReviseGoal(ctx, request)
	if err != nil && !errors.Is(err, authz.ErrDenied) {
		t.Fatal(err)
	}
	if outcome.Verdict == VerdictPass {
		t.Fatalf("unbound adapter mutated: %+v", outcome)
	}
}
