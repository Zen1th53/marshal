package budget

import (
	"fmt"
	"math"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type Process string

const (
	ProcessBuild        Process = "PROCESS_05"
	ProcessVerification Process = "PROCESS_06"
)

// CallRequest is a pre-dispatch reservation. Bounds are provider-enforced
// upper bounds for this call, never estimates or account quota. Unknown bounds
// refuse a call under the corresponding token/cost ceiling.
type CallRequest struct {
	RunID      string        `json:"run_id,omitempty"`
	Key        string        `json:"key"`
	Process    Process       `json:"process"`
	MaxTokens  *int64        `json:"max_tokens,omitempty"`
	MaxCostUSD *float64      `json:"max_cost_usd,omitempty"`
	Duration   time.Duration `json:"duration,omitempty"`
	Handoffs   int           `json:"handoffs,omitempty"`
	Retry      bool          `json:"retry,omitempty"`
}

func CheckDispatch(l BudgetLimit, c ConsumedBudget, r CallRequest) error {
	if err := l.Validate(); err != nil {
		return err
	}
	if c.ModelCalls < 0 || c.VerificationCalls < 0 || c.VerificationCalls > c.ModelCalls || c.Handoffs < 0 || c.Retries < 0 || c.Duration < 0 {
		return fmt.Errorf("%w: invalid cumulative budget", model.ErrInvalid)
	}

	if r.Key == "" || len(r.Key) > 256 || len(r.RunID) > 256 || r.Process != ProcessBuild && r.Process != ProcessVerification || r.Handoffs < 0 || r.Duration < 0 || r.MaxTokens != nil && *r.MaxTokens < 0 || r.MaxCostUSD != nil && (*r.MaxCostUSD < 0 || math.IsNaN(*r.MaxCostUSD) || math.IsInf(*r.MaxCostUSD, 0)) {
		return fmt.Errorf("%w: invalid call reservation", model.ErrInvalid)
	}
	tokens := c.TotalTokens
	if c.ReservedTotalTokens != nil && (tokens == nil || *c.ReservedTotalTokens > *tokens || c.UnreportedTokens) {
		tokens = c.ReservedTotalTokens
	}
	cost := c.CostUSD
	if c.ReservedCostUSD != nil && (cost == nil || *c.ReservedCostUSD > *cost || c.UnreportedCost) {
		cost = c.ReservedCostUSD
	}
	if l.MaxTotalTokens != nil {
		if tokens == nil || c.UnreportedTokens && c.ReservedTotalTokens == nil || r.MaxTokens == nil {
			return fmt.Errorf("%s: token usage or next-call bound UNKNOWN", model.ReasonBudgetExhaustedTokens)
		}
		if *tokens < 0 || *r.MaxTokens > *l.MaxTotalTokens || *tokens > *l.MaxTotalTokens-*r.MaxTokens {
			return fmt.Errorf("%s: token ceiling would be exceeded", model.ReasonBudgetExhaustedTokens)
		}
	}
	if l.MaxCostUSD != nil {
		if cost == nil || c.UnreportedCost && c.ReservedCostUSD == nil || r.MaxCostUSD == nil {
			return fmt.Errorf("%s: cost usage or next-call bound UNKNOWN", model.ReasonBudgetExhaustedCost)
		}
		if *cost < 0 || math.IsNaN(*cost) || math.IsInf(*cost, 0) || *r.MaxCostUSD > *l.MaxCostUSD || *cost > *l.MaxCostUSD-*r.MaxCostUSD {
			return fmt.Errorf("%s: cost ceiling would be exceeded", model.ReasonBudgetExhaustedCost)
		}
	}
	if l.MaxModelCalls > 0 {
		ceiling := l.MaxModelCalls
		if r.Process == ProcessBuild {
			remaining := l.VerificationReserveCalls - c.VerificationCalls
			if remaining > 0 {
				ceiling -= remaining
			}
		}
		if c.ModelCalls >= ceiling {
			return fmt.Errorf("%s: model-call ceiling or verification reserve reached", model.ReasonBudgetExhaustedCalls)
		}
	}
	duration := c.Duration
	if r.Duration > duration {
		duration = r.Duration
	}
	if l.MaxDuration > 0 && duration >= l.MaxDuration {
		return fmt.Errorf("%s: duration reached", model.ReasonBudgetExhaustedWallClock)
	}
	if l.MaxHandoffs > 0 && (r.Handoffs > l.MaxHandoffs || c.Handoffs > l.MaxHandoffs-r.Handoffs) {
		return fmt.Errorf("%s: handoff ceiling reached", model.ReasonBudgetExhaustedHandoffs)
	}
	if l.MaxRetries > 0 && r.Retry && c.Retries >= l.MaxRetries {
		return fmt.Errorf("%s: retry ceiling reached", model.ReasonBudgetExhaustedRetries)
	}
	if c.ModelCalls == math.MaxInt || r.Process == ProcessVerification && c.VerificationCalls == math.MaxInt || r.Retry && c.Retries == math.MaxInt || c.Handoffs > math.MaxInt-r.Handoffs {
		return fmt.Errorf("%w: budget counter overflow", model.ErrInvalid)
	}
	return nil
}

// CallSettlement records only explicit usage evidence and elapsed run time.
// Nil tokens/cost remain UNKNOWN. Call counts were charged at admission.
type CallSettlement struct {
	RunID       string        `json:"run_id"`
	Duration    time.Duration `json:"duration"`
	TotalTokens *int64        `json:"total_tokens,omitempty"`
	CostUSD     *float64      `json:"cost_usd,omitempty"`
}
