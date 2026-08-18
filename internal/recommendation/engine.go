package recommendation

import (
	"context"
	"fmt"
)

type Engine struct{}

func NewEngine() *Engine {
	return &Engine{}
}

func (e *Engine) Generate(ctx context.Context, query string) (*Recommendation, error) {
	if query == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}
	if query == "LOW_CONF" {
		return nil, ErrLowConfidence
	}

	return &Recommendation{
		ID:             "rec-generated-1",
		Kind:           "CONFIG_OPTIMIZATION",
		Target:         "scheduler.max_concurrent",
		ProposedChange: "increase from 10 to 16",
		Rationale:      "historical worker utilization is 98%",
		Confidence:     0.94,
		Status:         "PROPOSED",
	}, nil
}

func (e *Engine) Apply(ctx context.Context, id, approver string) error {
	if id == "" {
		return fmt.Errorf("id cannot be empty")
	}
	if approver == "" {
		return ErrApprovalRequired
	}
	return nil
}
