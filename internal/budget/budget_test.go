package budget_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/budget"
	"github.com/Zen1th53/marshal/internal/epistemic"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestBudgetExhaustionProducesResumablePartialWithCheckpoint(t *testing.T) {
	ctx := context.Background()
	eval := budget.NewEvaluator()
	tracker := budget.NewTracker()

	toks1 := int64(6000)
	toks2 := int64(5000)
	tracker.RecordUsage(adapter.Usage{
		Reported:    true,
		TotalTokens: &toks1,
	}, 2*time.Second, false, false)

	tracker.RecordUsage(adapter.Usage{
		Reported:    true,
		TotalTokens: &toks2,
	}, 3*time.Second, true, false)

	maxToks := int64(10000)
	limits := budget.BudgetLimit{
		MaxTotalTokens: &maxToks,
	}

	goal := model.GoalContract{
		ID:                 "goal-exh-1",
		SessionID:          "sess-exh-1",
		Revision:           1,
		UnderstandingState: model.GoalReady,
	}

	coverage := epistemic.CoverageReport{}
	ckptID := "ckpt-durable-999"

	term := eval.EvaluateTermination(ctx, goal, nil, coverage, tracker, limits, false, ckptID)

	if term.State != budget.StatePartial {
		t.Fatalf("expected state PARTIAL on budget exhaustion, got %v", term.State)
	}
	if term.ReasonCode != budget.ReasonBudgetExhaustedTokens {
		t.Fatalf("expected reason BUDGET_EXHAUSTED_TOKENS, got %v", term.ReasonCode)
	}
	if term.CheckpointID != ckptID {
		t.Fatalf("expected checkpoint %s, got %s", ckptID, term.CheckpointID)
	}
	if term.ConsumedBudget.TotalTokens == nil || *term.ConsumedBudget.TotalTokens != 11000 {
		t.Fatalf("expected 11000 consumed tokens, got %v", term.ConsumedBudget.TotalTokens)
	}
}

func TestUserCancellationPreservesState(t *testing.T) {
	ctx := context.Background()
	eval := budget.NewEvaluator()
	tracker := budget.NewTracker()

	goal := model.GoalContract{
		ID:                 "goal-cancel-1",
		SessionID:          "sess-cancel-1",
		Revision:           1,
		UnderstandingState: model.GoalReady,
	}

	term := eval.EvaluateTermination(ctx, goal, nil, epistemic.CoverageReport{}, tracker, budget.BudgetLimit{}, true, "ckpt-cancel-1")

	if term.State != budget.StateCancelled {
		t.Fatalf("expected state CANCELLED, got %v", term.State)
	}
	if term.ReasonCode != budget.ReasonUserCancelled {
		t.Fatalf("expected reason USER_CANCELLED, got %v", term.ReasonCode)
	}
	if term.CheckpointID != "ckpt-cancel-1" {
		t.Fatalf("expected checkpoint preserved, got %s", term.CheckpointID)
	}
}

func TestUnresolvedHighRiskDecisionBlocksExecution(t *testing.T) {
	ctx := context.Background()
	eval := budget.NewEvaluator()
	tracker := budget.NewTracker()

	goal := model.GoalContract{
		ID:                 "goal-block-1",
		SessionID:          "sess-block-1",
		Revision:           1,
		UnderstandingState: model.GoalNeedsInput,
		UnresolvedDecisions: []model.UnresolvedDecision{
			{
				ID:           "dec-auth-1",
				Question:     "Should we allow unauthenticated access to legacy API?",
				Impact:       "Severe security compromise risk",
				RequiresUser: true,
			},
		},
	}

	term := eval.EvaluateTermination(ctx, goal, nil, epistemic.CoverageReport{}, tracker, budget.BudgetLimit{}, false, "")

	if term.State != budget.StateBlocked {
		t.Fatalf("expected state BLOCKED, got %v", term.State)
	}
	if term.ReasonCode != budget.ReasonHighRiskDecisionPending {
		t.Fatalf("expected reason HIGH_RISK_DECISION_PENDING, got %v", term.ReasonCode)
	}
}

func TestMissingCriticalClaimBlocksSuccess(t *testing.T) {
	ctx := context.Background()
	eval := budget.NewEvaluator()
	tracker := budget.NewTracker()

	goal := model.GoalContract{
		ID:                 "goal-miss-1",
		SessionID:          "sess-miss-1",
		Revision:           1,
		UnderstandingState: model.GoalReady,
	}

	coverage := epistemic.CoverageReport{
		MissingClaims: []string{"Database migration rollbacks verified"},
	}

	term := eval.EvaluateTermination(ctx, goal, nil, coverage, tracker, budget.BudgetLimit{}, false, "")

	if term.State != budget.StatePartial {
		t.Fatalf("expected state PARTIAL when critical claim missing, got %v", term.State)
	}
	if term.ReasonCode != budget.ReasonCriticalClaimMissing {
		t.Fatalf("expected reason CRITICAL_CLAIM_MISSING, got %v", term.ReasonCode)
	}
}

func TestUnverifiedCriticalClaimBlocksSuccess(t *testing.T) {
	ctx := context.Background()
	eval := budget.NewEvaluator()
	tracker := budget.NewTracker()

	goal := model.GoalContract{
		ID:                 "goal-unver-1",
		SessionID:          "sess-unver-1",
		Revision:           1,
		UnderstandingState: model.GoalReady,
	}

	claim := model.Claim{
		ID:          "claim-crit-001",
		Subject:     "All security tests pass",
		Criticality: model.CriticalityBlocker,
		State:       model.ClaimStateSupported, // Supported but NOT Verified
	}

	coverage := epistemic.CoverageReport{
		UnverifiedCritical: []model.Claim{claim},
	}

	term := eval.EvaluateTermination(ctx, goal, []model.Claim{claim}, coverage, tracker, budget.BudgetLimit{}, false, "")

	if term.State != budget.StatePartial {
		t.Fatalf("expected state PARTIAL when critical claim unverified, got %v", term.State)
	}
	if term.ReasonCode != budget.ReasonCriticalClaimMissing {
		t.Fatalf("expected reason CRITICAL_CLAIM_MISSING, got %v", term.ReasonCode)
	}
}

func TestContradictedClaimBlocksSuccess(t *testing.T) {
	ctx := context.Background()
	eval := budget.NewEvaluator()
	tracker := budget.NewTracker()

	goal := model.GoalContract{
		ID:                 "goal-cont-1",
		SessionID:          "sess-cont-1",
		Revision:           1,
		UnderstandingState: model.GoalReady,
	}

	claim := model.Claim{
		ID:          "claim-crit-002",
		Subject:     "Schema migration 76 is idempotent",
		Criticality: model.CriticalityBlocker,
		State:       model.ClaimStateContested,
		StateReason: "Duplicate migration run failed with table already exists error",
	}

	coverage := epistemic.CoverageReport{
		ContestedClaims: []model.Claim{claim},
	}

	term := eval.EvaluateTermination(ctx, goal, []model.Claim{claim}, coverage, tracker, budget.BudgetLimit{}, false, "")

	if term.State != budget.StateBlocked {
		t.Fatalf("expected state BLOCKED when contradiction exists, got %v", term.State)
	}
	if term.ReasonCode != budget.ReasonUnresolvedContradiction {
		t.Fatalf("expected reason UNRESOLVED_CONTRADICTION, got %v", term.ReasonCode)
	}
}

func TestUnknownTokenUsagePreservedAsNil(t *testing.T) {
	tracker := budget.NewTracker()

	// Provider that does not report tokens/cost
	tracker.RecordUsage(adapter.Usage{
		Reported: false,
	}, 1200*time.Millisecond, false, false)

	consumed := tracker.Consumed()

	if consumed.TotalTokens != nil {
		t.Fatalf("expected TotalTokens to remain nil, got %v", *consumed.TotalTokens)
	}
	if consumed.CostUSD != nil {
		t.Fatalf("expected CostUSD to remain nil, got %v", *consumed.CostUSD)
	}
	if consumed.HasReportedTokens {
		t.Fatalf("expected HasReportedTokens to be false")
	}
	if consumed.ModelCalls != 1 {
		t.Fatalf("expected 1 model call, got %d", consumed.ModelCalls)
	}
}

func TestModelSwitchPreservesCumulativeUsage(t *testing.T) {
	tracker := budget.NewTracker()

	// Turn 1: Claude reports 1500 tokens, $0.015
	toks1 := int64(1500)
	cost1 := 0.015
	tracker.RecordUsage(adapter.Usage{
		Reported:    true,
		TotalTokens: &toks1,
		CostUSD:     &cost1,
	}, 1*time.Second, false, false)

	// Turn 2: Codex reports unknown tokens/cost
	tracker.RecordUsage(adapter.Usage{
		Reported: false,
	}, 2*time.Second, true, false)

	// Turn 3: Antigravity reports 850 tokens, $0.008
	toks3 := int64(850)
	cost3 := 0.008
	tracker.RecordUsage(adapter.Usage{
		Reported:    true,
		TotalTokens: &toks3,
		CostUSD:     &cost3,
	}, 1500*time.Millisecond, true, false)

	consumed := tracker.Consumed()

	if consumed.TotalTokens == nil || *consumed.TotalTokens != 2350 {
		t.Fatalf("expected 2350 tokens, got %v", consumed.TotalTokens)
	}
	if consumed.CostUSD == nil || *consumed.CostUSD < 0.0229 || *consumed.CostUSD > 0.0231 {
		t.Fatalf("expected ~$0.023, got %v", consumed.CostUSD)
	}
	if consumed.ModelCalls != 3 {
		t.Fatalf("expected 3 model calls, got %d", consumed.ModelCalls)
	}
	if consumed.Handoffs != 2 {
		t.Fatalf("expected 2 handoffs, got %d", consumed.Handoffs)
	}
	if !consumed.HasReportedTokens {
		t.Fatalf("expected HasReportedTokens to be true")
	}
}

func TestSuccessRequiresAllCriteriaMet(t *testing.T) {
	ctx := context.Background()
	eval := budget.NewEvaluator()
	tracker := budget.NewTracker()

	goal := model.GoalContract{
		ID:                 "goal-ok-1",
		SessionID:          "sess-ok-1",
		Revision:           1,
		UnderstandingState: model.GoalReady,
	}

	coverage := epistemic.CoverageReport{
		CriticalClaims:     2,
		VerifiedCritical:   2,
		MissingClaims:      nil,
		ContestedClaims:    nil,
		UnverifiedCritical: nil,
		CanSucceed:         true,
	}

	term := eval.EvaluateTermination(ctx, goal, nil, coverage, tracker, budget.BudgetLimit{}, false, "")

	if term.State != budget.StateSuccess {
		t.Fatalf("expected state SUCCESS when all criteria met, got %v", term.State)
	}
	if term.ReasonCode != budget.ReasonGoalAchieved {
		t.Fatalf("expected reason GOAL_ACHIEVED, got %v", term.ReasonCode)
	}
}
