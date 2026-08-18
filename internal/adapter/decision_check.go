package adapter

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/memory/decision"
)

type DecisionAdapter struct {
	engine *decision.Engine
}

func NewDecisionAdapter(engine *decision.Engine) *DecisionAdapter {
	return &DecisionAdapter{engine: engine}
}

func (a *DecisionAdapter) SubmitADR(ctx context.Context, id, taskID, agentID, title, contextStr, decStr string) (*decision.DecisionRecord, error) {
	if a == nil || a.engine == nil {
		return nil, fmt.Errorf("decision adapter uninitialized")
	}
	return a.engine.Propose(ctx, id, taskID, agentID, title, contextStr, decStr)
}
