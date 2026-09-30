package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestReviseGoalUsesCanonicalCASAndPreservesConstraints(t *testing.T) {
	ctx := context.Background()
	runtime := runtimeForPlan(t)
	goal := planGoal()
	project, err := runtime.Store().Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	goal.ProjectID = project.ID
	goal.ConstitutionVersion = constitution.Current.String()
	goal.Constraints = []model.Constraint{{ID: "hard-no-api", Text: "do not change the API", IsHard: true, Source: "operator"}}
	if err := runtime.Store().SaveGoalContract(ctx, goal, 1); err != nil {
		t.Fatal(err)
	}
	local, err := runtime.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ctx = local.Context(ctx)
	envelope := CommandEnvelope{ProjectID: project.ID, SessionID: goal.SessionID, TargetID: goal.ID, ExpectedVersion: 2, IdempotencyKey: "preserve-constraints"}
	revised, err := runtime.CommandReviseGoal(ctx, envelope, "correct the typo only", "operator narrowed wording")
	if err != nil {
		t.Fatal(err)
	}
	if revised.Revision != 3 || revised.Confirmation != model.ConfirmationPending || revised.DesiredOutcome != "correct the typo only" || len(revised.Constraints) != 1 || revised.Constraints[0].ID != "hard-no-api" {
		t.Fatalf("revised goal = %+v", revised)
	}
	envelope.IdempotencyKey = "stale-constraints"
	if _, err := runtime.CommandReviseGoal(ctx, envelope, "stale", "stale write"); !errors.Is(err, model.ErrGoalConflict) {
		t.Fatalf("stale revision error = %v, want goal conflict", err)
	}
}
