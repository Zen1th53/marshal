package app

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// PolicyReadback separates what is configured from what the runtime has
// observably done. Nothing here asserts enforcement it cannot show.
type PolicyReadback struct {
	// RuntimePolicy is "<id> v<version>" when a policy governs runs, or empty.
	RuntimePolicy string
	// GateEngine is "configured" or "none".
	GateEngine string
	Gates      store.GateDecisionSummary
}

func (r *Runtime) PolicyReadback(ctx context.Context) (PolicyReadback, error) {
	if r == nil || r.store == nil {
		return PolicyReadback{}, model.ErrUnavailable
	}
	var out PolicyReadback
	if r.policyConfigured {
		out.RuntimePolicy = fmt.Sprintf("%s v%d", r.runtimePolicy.PolicyID, r.runtimePolicy.PolicyVersion)
	}
	switch {
	case r.gateEngine == nil:
		out.GateEngine = "none"
	default:
		out.GateEngine = "configured"
	}
	summary, err := r.store.SummarizeGateDecisions(ctx)
	if err != nil {
		return PolicyReadback{}, err
	}
	out.Gates = summary
	return out, nil
}
