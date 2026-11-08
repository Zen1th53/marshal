package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/budget"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestVerificationReserveSurvivesGoalRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	g := model.GoalContract{ID: "g", SessionID: "s", ProjectID: "p", Revision: 1, DesiredOutcome: "x", Risk: model.R1, AuthoritySource: "owner", Confirmation: model.ConfirmationApproved, BudgetLimits: &model.BudgetLimit{MaxModelCalls: 3, VerificationReserveCalls: 1}}
	if err := s.SaveGoalContract(ctx, g, 0); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"build1", "build2"} {
		if ok, err := s.ReserveGoalCall(ctx, "s", "g", 1, budget.CallRequest{Key: key, Process: budget.ProcessBuild}); err != nil || !ok {
			t.Fatalf("reserve %v %v", ok, err)
		}
	}
	if _, err := s.ReserveGoalCall(ctx, "s", "g", 1, budget.CallRequest{Key: "build3", Process: budget.ProcessBuild}); err == nil {
		t.Fatal("build spent reserve")
	}
	if ok, err := s.ReserveGoalCall(ctx, "s", "g", 1, budget.CallRequest{Key: "verify", Process: budget.ProcessVerification}); err != nil || !ok {
		t.Fatalf("verify %v %v", ok, err)
	}
	if ok, err := s.ReserveGoalCall(ctx, "s", "g", 1, budget.CallRequest{Key: "verify", Process: budget.ProcessVerification}); err != nil || ok {
		t.Fatalf("replay %v %v", ok, err)
	}
	if _, err := s.ReserveGoalCall(ctx, "s", "g", 1, budget.CallRequest{Key: "verify", Process: budget.ProcessBuild}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("changed replay %v", err)
	}
	g.Revision = 2
	g.Confirmation = model.ConfirmationPending
	if err := s.SaveGoalContract(ctx, g, 1); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetGoalContract(ctx, "g", 2)
	if err != nil || loaded.BudgetLimits.VerificationReserveCalls != 1 {
		t.Fatalf("round trip %+v %v", loaded, err)
	}
	used, err := s.GetBudgetTracker(ctx, "s", "g", 2)
	if err != nil || used.ModelCalls != 3 || used.VerificationCalls != 1 || used.TotalTokens != nil || used.CostUSD != nil {
		t.Fatalf("usage %+v %v", used, err)
	}
}

func TestBudgetReservationUsesKnownBoundsWithoutInventingMeasurements(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	tokens := int64(10)
	cost := float64(1)
	g := model.GoalContract{ID: "g", SessionID: "s", ProjectID: "p", Revision: 1, DesiredOutcome: "x", Risk: model.R1, AuthoritySource: "owner", Confirmation: model.ConfirmationApproved, BudgetLimits: &model.BudgetLimit{MaxTotalTokens: &tokens, MaxCostUSD: &cost}}
	if err := s.SaveGoalContract(ctx, g, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReserveGoalCall(ctx, "s", "g", 1, budget.CallRequest{Key: "unknown", Process: budget.ProcessBuild}); err == nil {
		t.Fatal("unknown call admitted")
	}
	zeroTokens := int64(0)
	zeroCost := float64(0)
	if ok, err := s.ReserveGoalCall(ctx, "s", "g", 1, budget.CallRequest{Key: "known-zero", Process: budget.ProcessBuild, MaxTokens: &zeroTokens, MaxCostUSD: &zeroCost}); err != nil || !ok {
		t.Fatalf("known zero refused %v %v", ok, err)
	}
	used, _ := s.GetBudgetTracker(ctx, "s", "g", 1)
	if used.TotalTokens != nil || used.CostUSD != nil || used.HasReportedTokens || used.HasReportedCost {
		t.Fatalf("invented measurements %+v", used)
	}
	tokensPerCall := int64(6)
	costPerCall := float64(0.6)
	if ok, err := s.ReserveGoalCall(ctx, "s", "g", 1, budget.CallRequest{Key: "bound1", Process: budget.ProcessBuild, MaxTokens: &tokensPerCall, MaxCostUSD: &costPerCall}); err != nil || !ok {
		t.Fatalf("bound %v %v", ok, err)
	}
	if _, err := s.ReserveGoalCall(ctx, "s", "g", 1, budget.CallRequest{Key: "bound2", Process: budget.ProcessBuild, MaxTokens: &tokensPerCall, MaxCostUSD: &costPerCall}); err == nil {
		t.Fatal("exceeded bounds")
	}
}

func TestBudgetDurationAndLateSettlementSurviveGoalCancellation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	g := model.GoalContract{ID: "g", SessionID: "s", ProjectID: "p", Revision: 1, DesiredOutcome: "x", Risk: model.R1, AuthoritySource: "owner", Confirmation: model.ConfirmationApproved, BudgetLimits: &model.BudgetLimit{MaxDuration: 10 * time.Second}}
	if err := s.SaveGoalContract(ctx, g, 0); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ReserveGoalCall(ctx, "s", "g", 1, budget.CallRequest{Key: "a", RunID: "run1", Process: budget.ProcessBuild, Duration: time.Second}); err != nil || !ok {
		t.Fatalf("reserve %v %v", ok, err)
	}
	if _, err := s.CancelGoalContract(ctx, "s", "g", 1, "stop"); err != nil {
		t.Fatal(err)
	}
	settled := budget.CallSettlement{RunID: "run1", Duration: 5 * time.Second}
	if err := s.SettleGoalCall(ctx, "s", "g", 1, "a", settled); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleGoalCall(ctx, "s", "g", 1, "a", settled); err != nil {
		t.Fatal(err)
	}
	used, err := s.GetBudgetTracker(ctx, "s", "g", 2)
	if err != nil || used.Duration != 5*time.Second || used.ModelCalls != 1 {
		t.Fatalf("late usage %+v %v", used, err)
	}
	term, err := s.GetGoalTermination(ctx, "s", "g", 2)
	if err != nil || term.State != model.StateCancelled || term.ConsumedBudget.Duration != 5*time.Second {
		t.Fatalf("cancel term %+v %v", term, err)
	}
}
