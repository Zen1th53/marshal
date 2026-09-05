package budget

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/epistemic"
	"github.com/Zen1th53/marshal/internal/model"
)

// Evaluator determines the definitive 5-state product termination.
type Evaluator struct{}

func NewEvaluator() *Evaluator {
	return &Evaluator{}
}

// EvaluateTermination decides the unambiguous top-level product state.
// Principle: DONE != SUCCESS.
func (e *Evaluator) EvaluateTermination(
	ctx context.Context,
	goal model.GoalContract,
	claims []model.Claim,
	coverage epistemic.CoverageReport,
	tracker *Tracker,
	limits BudgetLimit,
	isCancelled bool,
	checkpointID string,
) GoalTermination {
	now := time.Now().UTC()
	consumed := tracker.Consumed()

	term := GoalTermination{
		SessionID:      goal.SessionID,
		GoalID:         goal.ID,
		GoalRevision:   goal.Revision,
		ConsumedBudget: consumed,
		CheckpointID:   checkpointID,
		CompletedAt:    now,
	}

	// 1. User Cancellation
	if isCancelled {
		term.State = StateCancelled
		term.ReasonCode = ReasonUserCancelled
		term.ReasonDetail = "Task execution was cancelled by user; state preserved without claiming success or failure"
		return term
	}

	// 2. Budget Exhaustion
	// Exact budget exhaustion produces a resumable PARTIAL state with durable checkpoint
	if isExhausted, dimension, reason := tracker.CheckExhaustion(limits); isExhausted {
		term.State = StatePartial
		term.ReasonCode = reason
		term.ReasonDetail = fmt.Sprintf("Operational budget ceiling reached for %s; resumable checkpoint %s saved",
			dimension, checkpointID)
		return term
	}

	// 3. Unresolved High-Risk Decision or Understanding State BLOCKED
	if goal.UnderstandingState == model.GoalNeedsInput || len(goal.UnresolvedDecisions) > 0 {
		detail := "Unresolved decision requires user input before work can proceed safely"
		if len(goal.UnresolvedDecisions) > 0 {
			detail = fmt.Sprintf("Unresolved high-risk decision: %s", goal.UnresolvedDecisions[0].Question)
		}
		term.State = StateBlocked
		term.ReasonCode = ReasonHighRiskDecisionPending
		term.ReasonDetail = detail
		return term
	}

	// 4. Critical-Claim Coverage & Contradiction Invariants
	// Unresolved contradiction blocks success
	if len(coverage.ContestedClaims) > 0 {
		term.State = StateBlocked
		term.ReasonCode = ReasonUnresolvedContradiction
		term.ReasonDetail = fmt.Sprintf("Unresolved contradiction in claim %s: %s",
			coverage.ContestedClaims[0].ID, coverage.ContestedClaims[0].StateReason)
		return term
	}

	// Missing critical claim blocks SUCCESS
	if len(coverage.MissingClaims) > 0 {
		term.State = StatePartial
		term.ReasonCode = ReasonCriticalClaimMissing
		term.ReasonDetail = fmt.Sprintf("All code written but required critical claims missing: %s",
			strings.Join(coverage.MissingClaims, ", "))
		return term
	}

	// Unverified critical claim blocks SUCCESS
	if len(coverage.UnverifiedCritical) > 0 {
		c := coverage.UnverifiedCritical[0]
		term.State = StatePartial
		term.ReasonCode = ReasonCriticalClaimMissing
		term.ReasonDetail = fmt.Sprintf("Critical claim %s is %s (not VERIFIED): %s",
			c.ID, c.State, c.Subject)
		return term
	}

	// 5. SUCCESS: Goal criteria met, critical claims verified, constraints intact, budget satisfied
	term.State = StateSuccess
	term.ReasonCode = ReasonGoalAchieved
	term.ReasonDetail = "Goal successfully completed: all criteria met, critical claims verified, zero contradictions"
	return term
}
