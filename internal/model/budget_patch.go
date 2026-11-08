package model

import (
	"fmt"
	"math"
	"time"
)

type BudgetDimension string

const (
	BudgetTokens   BudgetDimension = "tokens"
	BudgetCost     BudgetDimension = "cost"
	BudgetDuration BudgetDimension = "duration"
	BudgetCalls    BudgetDimension = "calls"
	BudgetHandoffs BudgetDimension = "handoffs"
	BudgetRetries  BudgetDimension = "retries"
	BudgetReserve  BudgetDimension = "verification_reserve"
)

// BudgetPatch changes exactly one limit. Clear is distinct from an absent
// patch and from a measured zero token/cost ceiling. Count/duration zero use
// the legacy unlimited encoding, so callers must use Clear for those fields.
type BudgetPatch struct {
	Dimension BudgetDimension `json:"dimension"`
	Clear     bool            `json:"clear,omitempty"`
	Tokens    int64           `json:"tokens,omitempty"`
	CostUSD   float64         `json:"cost_usd,omitempty"`
	Count     int             `json:"count,omitempty"`
	Duration  time.Duration   `json:"duration,omitempty"`
}

func (l BudgetLimit) Validate() error {
	if l.MaxTotalTokens != nil && *l.MaxTotalTokens < 0 || l.MaxCostUSD != nil && (*l.MaxCostUSD < 0 || math.IsNaN(*l.MaxCostUSD) || math.IsInf(*l.MaxCostUSD, 0)) || l.MaxDuration < 0 || l.MaxModelCalls < 0 || l.MaxHandoffs < 0 || l.MaxRetries < 0 || l.VerificationReserveCalls < 0 {
		return fmt.Errorf("%w: limits must be finite and nonnegative", ErrGoalInvalid)
	}
	if l.VerificationReserveCalls > 0 && (l.MaxModelCalls == 0 || l.VerificationReserveCalls > l.MaxModelCalls) {
		return fmt.Errorf("%w: verification reserve requires a model-call ceiling at least as large", ErrGoalInvalid)
	}
	return nil
}

func (l BudgetLimit) Empty() bool {
	return l.MaxTotalTokens == nil && l.MaxCostUSD == nil && l.MaxDuration == 0 && l.MaxModelCalls == 0 && l.MaxHandoffs == 0 && l.MaxRetries == 0 && l.VerificationReserveCalls == 0
}

func (p BudgetPatch) Apply(previous *BudgetLimit) (*BudgetLimit, error) {
	l := BudgetLimit{}
	if previous != nil {
		l = *previous
	}
	// Reject unrelated payload fields rather than silently dropping them.
	if p.Tokens != 0 && p.Dimension != BudgetTokens || p.CostUSD != 0 && p.Dimension != BudgetCost || p.Duration != 0 && p.Dimension != BudgetDuration || p.Count != 0 && p.Dimension != BudgetCalls && p.Dimension != BudgetHandoffs && p.Dimension != BudgetRetries && p.Dimension != BudgetReserve || p.Clear && (p.Tokens != 0 || p.CostUSD != 0 || p.Duration != 0 || p.Count != 0) {
		return nil, fmt.Errorf("%w: patch must change one field", ErrGoalInvalid)
	}
	switch p.Dimension {
	case BudgetTokens:
		if p.Clear {
			l.MaxTotalTokens = nil
		} else {
			v := p.Tokens
			l.MaxTotalTokens = &v
		}
	case BudgetCost:
		if p.Clear {
			l.MaxCostUSD = nil
		} else {
			v := p.CostUSD
			l.MaxCostUSD = &v
		}
	case BudgetDuration:
		if p.Clear {
			l.MaxDuration = 0
		} else if p.Duration <= 0 {
			return nil, ErrGoalInvalid
		} else {
			l.MaxDuration = p.Duration
		}
	case BudgetCalls, BudgetHandoffs, BudgetRetries, BudgetReserve:
		v := p.Count
		if p.Clear {
			v = 0
		} else if v <= 0 {
			return nil, ErrGoalInvalid
		}
		switch p.Dimension {
		case BudgetCalls:
			l.MaxModelCalls = v
		case BudgetHandoffs:
			l.MaxHandoffs = v
		case BudgetRetries:
			l.MaxRetries = v
		case BudgetReserve:
			l.VerificationReserveCalls = v
		}
	default:
		return nil, fmt.Errorf("%w: unknown budget dimension", ErrGoalInvalid)
	}
	if err := l.Validate(); err != nil {
		return nil, err
	}
	if l.Empty() {
		return nil, nil
	}
	return &l, nil
}
