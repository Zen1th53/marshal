package worker

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/capability"
)

// capabilityGate is the privileged process boundary. It authorizes before
// delegating to a runner and contains no process-launch fallback.
type capabilityGate struct {
	runner adapter.ProcessRunner
	broker capability.Broker
	query  func(adapter.Command) capability.Query
}

func NewCapabilityGate(runner adapter.ProcessRunner, broker capability.Broker, query func(adapter.Command) capability.Query) adapter.ProcessRunner {
	return &capabilityGate{runner: runner, broker: broker, query: query}
}

func (g *capabilityGate) Run(ctx context.Context, command adapter.Command) (adapter.ProcessResult, error) {
	if g.runner == nil || g.broker == nil || g.query == nil {
		return adapter.ProcessResult{}, capability.ErrDenied
	}
	decision, err := g.broker.Authorize(ctx, g.query(command))
	if err != nil {
		return adapter.ProcessResult{}, fmt.Errorf("%w: capability authority unavailable", capability.ErrDenied)
	}
	if err := decision.Validate(); err != nil || !decision.Allowed {
		return adapter.ProcessResult{}, capability.ErrDenied
	}
	return g.runner.Run(ctx, command)
}
