//go:build governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/netpolicy"
)

func buildAutomaticEgressSource(any) bool { return false }

func (e *storedRunEgress) buildEvaluateRunEgress(ctx context.Context, scope *runEgress, request netpolicy.Request, _ []events.Event) (netpolicy.Decision, error) {
	return scope.allowlist.Evaluate(ctx, request)
}
