//go:build governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/netpolicy"
)

func buildOpenEgressSource(any) bool { return false }

// Community keeps exact operator-granted egress: no destination is added
// without an operator decision.
func (e *storedRunEgress) buildEvaluateOpenEgress(ctx context.Context, scope *runEgress, request netpolicy.Request, history []events.Event) (netpolicy.Decision, error) {
	return e.buildEvaluateRunEgress(ctx, scope, request, history)
}

// AutomaticOpenEgressRequest never applies in Community: every destination
// outside the run's defaults needs an operator decision.
func (*Runtime) AutomaticOpenEgressRequest(context.Context, string, string) (string, bool) {
	return "", false
}
