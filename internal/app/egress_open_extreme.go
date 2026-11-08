//go:build !governed

package app

import (
	"context"
	"net"
	"strconv"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/netpolicy"
)

// extremeOpenEgressSource names the automatic grant for a destination that is
// not a registered provider host. The private edition never stops work to ask
// about network access: every TCP destination a governed run reaches through
// its proxy is granted, exactly and durably, before the first connection.
const extremeOpenEgressSource = "extreme open egress"

const extremeOpenEgressReason = "Marshal: network egress applies automatically"

func buildOpenEgressSource(source any) bool { return source == extremeOpenEgressSource }

// buildEvaluateOpenEgress keeps the provider-host decision first, so provider
// evidence is unchanged, then grants any remaining destination. An explicit
// operator revocation still wins, and a harness probe stays nonexpanding: it
// verifies a credential, it does no work.
func (e *storedRunEgress) buildEvaluateOpenEgress(ctx context.Context, scope *runEgress, request netpolicy.Request, history []events.Event) (netpolicy.Decision, error) {
	decision, err := e.buildEvaluateRunEgress(ctx, scope, request, history)
	if err != nil || decision.Allowed || scope.harnessProbe || request.Protocol != netpolicy.ProtocolTCP || e.scope == nil || e.scope.allowlist == nil {
		return decision, err
	}
	endpoint, err := netpolicy.Endpoint(net.JoinHostPort(request.Host, strconv.Itoa(request.Port)))
	if err != nil || providerEndpointRevoked(scope, endpoint, history) {
		return decision, nil
	}
	e.scope.mu.Lock()
	defer e.scope.mu.Unlock()
	if current, err := e.scope.allowlist.Evaluate(ctx, request); err != nil || current.Allowed {
		return current, err
	}
	err = e.scope.allowlist.Set(endpoint, true, func(normalized string) error {
		return e.runtime.recordEgress(ctx, scope, events.EventTypeNetworkEgressGranted, map[string]any{
			"endpoint": normalized, "actor": "runtime", "source": extremeOpenEgressSource,
			"decided_by": ExtremeRuntimeDecider, "reason": extremeOpenEgressReason, "allowed": true,
		})
	})
	if err != nil {
		return decision, err
	}
	return e.scope.allowlist.Evaluate(ctx, request)
}

// AutomaticOpenEgressRequest reports whether a queued refusal for a live run
// applies automatically. It covers refusals recorded before this edition
// granted every destination at first connection, and refusals from another
// runtime sharing the project store.
func (r *Runtime) AutomaticOpenEgressRequest(ctx context.Context, runID, endpoint string) (string, bool) {
	if r == nil || r.store == nil {
		return "", false
	}
	endpoint, err := netpolicy.Endpoint(endpoint)
	if err != nil {
		return "", false
	}
	history, err := r.store.EgressControlEvents(ctx, runID, 0)
	if err != nil {
		return "", false
	}
	scopes, err := projectEgressScopes(history)
	if err != nil {
		return "", false
	}
	scope := scopes[runID]
	if scope == nil || scope.harnessProbe || !scope.pending[endpoint] || providerEndpointRevoked(scope, endpoint, history) || !egressScopeLive(scope.socket) {
		return "", false
	}
	return scope.provider, true
}
