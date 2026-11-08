package app

import (
	"reflect"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestBudgetPatchAllMenuDimensionsPreserveDurableContract(t *testing.T) {
	for _, patch := range []model.BudgetPatch{
		{Dimension: model.BudgetTokens, Tokens: 1000},
		{Dimension: model.BudgetCost, CostUSD: 1.25},
		{Dimension: model.BudgetHandoffs, Count: 8},
		{Dimension: model.BudgetReserve, Count: 3},
	} {
		t.Run(string(patch.Dimension), func(t *testing.T) {
			r, ctx, envelope, _ := goalCancelFixture(t, model.ConfirmationApproved)
			tokens, cost := int64(500), 0.75
			limits := model.BudgetLimit{MaxTotalTokens: &tokens, MaxCostUSD: &cost, MaxModelCalls: 12, MaxHandoffs: 4, VerificationReserveCalls: 2}
			prior, err := r.CommandSetGoalBudget(ctx, envelope, limits)
			if err != nil {
				t.Fatal(err)
			}
			used := model.ConsumedBudget{ModelCalls: 3, VerificationCalls: 1}
			if err := r.store.SaveBudgetTracker(ctx, envelope.SessionID, envelope.TargetID, prior.Revision, used); err != nil {
				t.Fatal(err)
			}
			beforeUsage, err := r.store.GetBudgetTracker(ctx, envelope.SessionID, envelope.TargetID, prior.Revision)
			if err != nil {
				t.Fatal(err)
			}
			envelope.ExpectedVersion, envelope.IdempotencyKey = prior.Revision, "menu-budget-patch"
			revised, err := r.CommandPatchGoalBudget(ctx, envelope, patch, "change the selected menu limit")
			if err != nil {
				t.Fatal(err)
			}
			expected, err := patch.Apply(&limits)
			if err != nil {
				t.Fatal(err)
			}
			proof, err := r.store.GetGoalContract(ctx, revised.ID, revised.Revision)
			if err != nil || proof.Revision != prior.Revision+1 || proof.Confirmation != model.ConfirmationPending || !reflect.DeepEqual(proof.BudgetLimits, expected) {
				t.Fatalf("canonical pending budget revision: %+v %v", proof, err)
			}
			consumed, err := r.store.GetBudgetTracker(ctx, envelope.SessionID, envelope.TargetID, proof.Revision)
			if err != nil || !reflect.DeepEqual(consumed, beforeUsage) {
				t.Fatalf("usage reset by menu patch: %+v %v", consumed, err)
			}
			original, err := r.store.GetGoalContract(ctx, prior.ID, prior.Revision)
			if err != nil || !reflect.DeepEqual(original.BudgetLimits, &limits) {
				t.Fatalf("prior contract was mutated: %+v %v", original, err)
			}
			replay, err := r.CommandPatchGoalBudget(ctx, envelope, patch, "change the selected menu limit")
			if err != nil || replay.Revision != proof.Revision {
				t.Fatalf("patch replay created another revision: %+v %v", replay, err)
			}
		})
	}
}
