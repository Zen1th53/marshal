package recommendation

import (
	"context"
	"fmt"
	"sync"
)

type Engine struct {
	mu   sync.RWMutex
	recs map[string]*Recommendation
}

func NewEngine() *Engine {
	return &Engine{
		recs: make(map[string]*Recommendation),
	}
}

// Generate reports honest no-data in Wave 1 pending real failure-observation wiring.
func (e *Engine) Generate(ctx context.Context, query string) (*Recommendation, error) {
	if query == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}
	return nil, ErrNoData
}

// Apply applies an existing recommendation given valid human approval.
func (e *Engine) Apply(ctx context.Context, id, approver string) error {
	if id == "" {
		return fmt.Errorf("id cannot be empty")
	}
	if approver == "" {
		return ErrApprovalRequired
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	rec, ok := e.recs[id]
	if !ok {
		return ErrNotFound
	}
	rec.Status = "APPLIED"
	return nil
}
