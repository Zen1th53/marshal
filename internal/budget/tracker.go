package budget

import (
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
)

// Tracker maintains cumulative resource usage across turns, agents, and model switches.
type Tracker struct {
	mu       sync.RWMutex
	consumed ConsumedBudget
}

func NewTracker() *Tracker {
	return &Tracker{}
}

func NewTrackerWithConsumed(consumed ConsumedBudget) *Tracker {
	return &Tracker{consumed: consumed}
}

// RecordUsage accumulates resource metrics from adapter execution.
// Unknown usage remains unknown (nil), never synthesized to zero.
func (t *Tracker) RecordUsage(usage adapter.Usage, duration time.Duration, isHandoff bool, isRetry bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.consumed.Duration += duration
	t.consumed.ModelCalls++
	if isHandoff {
		t.consumed.Handoffs++
	}
	if isRetry {
		t.consumed.Retries++
	}

	if usage.Reported {
		if usage.TotalTokens != nil {
			if t.consumed.TotalTokens == nil {
				val := *usage.TotalTokens
				t.consumed.TotalTokens = &val
			} else {
				*t.consumed.TotalTokens += *usage.TotalTokens
			}
			t.consumed.HasReportedTokens = true
		}

		if usage.CostUSD != nil {
			if t.consumed.CostUSD == nil {
				val := *usage.CostUSD
				t.consumed.CostUSD = &val
			} else {
				*t.consumed.CostUSD += *usage.CostUSD
			}
			t.consumed.HasReportedCost = true
		}
	}
}

// CheckExhaustion evaluates whether any configured operational ceiling has been reached.
func (t *Tracker) CheckExhaustion(limits BudgetLimit) (bool, string, ReasonCode) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if limits.MaxTotalTokens != nil && t.consumed.TotalTokens != nil && *t.consumed.TotalTokens >= *limits.MaxTotalTokens {
		return true, "tokens", ReasonBudgetExhaustedTokens
	}

	if limits.MaxCostUSD != nil && t.consumed.CostUSD != nil && *t.consumed.CostUSD >= *limits.MaxCostUSD {
		return true, "cost", ReasonBudgetExhaustedCost
	}

	if limits.MaxDuration > 0 && t.consumed.Duration >= limits.MaxDuration {
		return true, "wall_clock", ReasonBudgetExhaustedWallClock
	}

	if limits.MaxModelCalls > 0 && t.consumed.ModelCalls >= limits.MaxModelCalls {
		return true, "model_calls", ReasonBudgetExhaustedCalls
	}

	if limits.MaxHandoffs > 0 && t.consumed.Handoffs >= limits.MaxHandoffs {
		return true, "handoffs", ReasonBudgetExhaustedHandoffs
	}

	if limits.MaxRetries > 0 && t.consumed.Retries >= limits.MaxRetries {
		return true, "retries", ReasonBudgetExhaustedRetries
	}

	return false, "", ""
}

// Consumed returns a snapshot of cumulative resource consumption.
func (t *Tracker) Consumed() ConsumedBudget {
	t.mu.RLock()
	defer t.mu.RUnlock()

	copy := t.consumed
	if t.consumed.TotalTokens != nil {
		val := *t.consumed.TotalTokens
		copy.TotalTokens = &val
	}
	if t.consumed.CostUSD != nil {
		val := *t.consumed.CostUSD
		copy.CostUSD = &val
	}
	return copy
}
