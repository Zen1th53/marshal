package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// TerminationState represents the 5 non-negotiable product termination states.
type TerminationState string

const (
	StateSuccess         TerminationState = "SUCCESS"
	StatePartial         TerminationState = "PARTIAL"
	StateBlocked         TerminationState = "BLOCKED"
	StateBudgetExhausted TerminationState = "BUDGET_EXHAUSTED"
	StateCancelled       TerminationState = "CANCELLED"
)

var (
	ErrInvalidTerminationState = errors.New("invalid termination state: must be SUCCESS, PARTIAL, BLOCKED, BUDGET_EXHAUSTED, or CANCELLED")
)

func (s TerminationState) IsValid() bool {
	switch s {
	case StateSuccess, StatePartial, StateBlocked, StateBudgetExhausted, StateCancelled:
		return true
	default:
		return false
	}
}

// ReasonCode defines machine-readable structured reasons for termination states.
type ReasonCode string

const (
	ReasonGoalAchieved             ReasonCode = "GOAL_ACHIEVED"
	ReasonCriticalClaimMissing     ReasonCode = "CRITICAL_CLAIM_MISSING"
	ReasonUnresolvedContradiction  ReasonCode = "UNRESOLVED_CONTRADICTION"
	ReasonConstraintViolated       ReasonCode = "CONSTRAINT_VIOLATED"
	ReasonHighRiskDecisionPending  ReasonCode = "HIGH_RISK_DECISION_PENDING"
	ReasonUserCancelled            ReasonCode = "USER_CANCELLED"
	ReasonBudgetExhaustedTokens    ReasonCode = "BUDGET_EXHAUSTED_TOKENS"
	ReasonBudgetExhaustedCost      ReasonCode = "BUDGET_EXHAUSTED_COST"
	ReasonBudgetExhaustedWallClock ReasonCode = "BUDGET_EXHAUSTED_WALLCLOCK"
	ReasonBudgetExhaustedCalls     ReasonCode = "BUDGET_EXHAUSTED_CALLS"
	ReasonBudgetExhaustedHandoffs  ReasonCode = "BUDGET_EXHAUSTED_HANDOFFS"
	ReasonBudgetExhaustedRetries   ReasonCode = "BUDGET_EXHAUSTED_RETRIES"
)

// BudgetLimit defines operational ceilings across supported dimensions.
type BudgetLimit struct {
	MaxTotalTokens *int64        `json:"max_total_tokens,omitempty"`
	MaxCostUSD     *float64      `json:"max_cost_usd,omitempty"`
	MaxDuration    time.Duration `json:"max_duration,omitempty"`
	MaxModelCalls  int           `json:"max_model_calls,omitempty"`
	MaxHandoffs    int           `json:"max_handoffs,omitempty"`
	MaxRetries     int           `json:"max_retries,omitempty"`
}

// ConsumedBudget tracks actual cumulative resource consumption.
// Unknown usage remains nil/unreported, never synthesized to 0.
type ConsumedBudget struct {
	TotalTokens       *int64        `json:"total_tokens,omitempty"`
	CostUSD           *float64      `json:"cost_usd,omitempty"`
	Duration          time.Duration `json:"duration,omitempty"`
	ModelCalls        int           `json:"model_calls"`
	Handoffs          int           `json:"handoffs"`
	Retries           int           `json:"retries"`
	HasReportedTokens bool          `json:"has_reported_tokens"`
	HasReportedCost   bool          `json:"has_reported_cost"`
}

// GoalTermination records the definitive final product state for a Goal execution.
type GoalTermination struct {
	SessionID      string           `json:"session_id"`
	GoalID         string           `json:"goal_id"`
	GoalRevision   int64            `json:"goal_revision"`
	State          TerminationState `json:"state"`
	ReasonCode     ReasonCode       `json:"reason_code"`
	ReasonDetail   string           `json:"reason_detail"`
	ConsumedBudget ConsumedBudget   `json:"consumed_budget"`
	CheckpointID   string           `json:"checkpoint_id,omitempty"`
	CompletedAt    time.Time        `json:"completed_at"`
}

func (t GoalTermination) Validate() error {
	if !t.State.IsValid() {
		return fmt.Errorf("%w: %q", ErrInvalidTerminationState, t.State)
	}
	if strings.TrimSpace(t.SessionID) == "" || strings.TrimSpace(t.GoalID) == "" {
		return fmt.Errorf("%w: session ID and goal ID are required", ErrInvalid)
	}
	if strings.TrimSpace(string(t.ReasonCode)) == "" {
		return fmt.Errorf("%w: reason code is required", ErrInvalid)
	}
	if t.CompletedAt.IsZero() {
		return fmt.Errorf("%w: completed_at timestamp is required", ErrInvalid)
	}
	return nil
}
