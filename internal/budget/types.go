package budget

import (
	"errors"

	"github.com/Zen1th53/marshal/internal/model"
)

type TerminationState = model.TerminationState

const (
	StateSuccess         = model.StateSuccess
	StatePartial         = model.StatePartial
	StateBlocked         = model.StateBlocked
	StateBudgetExhausted = model.StateBudgetExhausted
	StateCancelled       = model.StateCancelled
)

var (
	ErrInvalidTerminationState = model.ErrInvalidTerminationState
	ErrDoneIsNotSuccess        = errors.New("DONE != SUCCESS: success requires critical claims, preserved constraints, and zero blocking contradictions")
)

type ReasonCode = model.ReasonCode

const (
	ReasonGoalAchieved             = model.ReasonGoalAchieved
	ReasonCriticalClaimMissing     = model.ReasonCriticalClaimMissing
	ReasonUnresolvedContradiction  = model.ReasonUnresolvedContradiction
	ReasonConstraintViolated       = model.ReasonConstraintViolated
	ReasonHighRiskDecisionPending  = model.ReasonHighRiskDecisionPending
	ReasonUserCancelled            = model.ReasonUserCancelled
	ReasonBudgetExhaustedTokens    = model.ReasonBudgetExhaustedTokens
	ReasonBudgetExhaustedCost      = model.ReasonBudgetExhaustedCost
	ReasonBudgetExhaustedWallClock = model.ReasonBudgetExhaustedWallClock
	ReasonBudgetExhaustedCalls     = model.ReasonBudgetExhaustedCalls
	ReasonBudgetExhaustedHandoffs  = model.ReasonBudgetExhaustedHandoffs
	ReasonBudgetExhaustedRetries   = model.ReasonBudgetExhaustedRetries
)

type BudgetLimit = model.BudgetLimit
type ConsumedBudget = model.ConsumedBudget
type GoalTermination = model.GoalTermination
