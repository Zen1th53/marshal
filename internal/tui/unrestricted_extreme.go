//go:build !governed

package tui

import (
	"context"
	"strings"

	"github.com/Zen1th53/marshal/internal/permission"
)

const (
	extremeNetworkReason        = "Marshal: network egress applies automatically"
	extremeNetworkClosedReason  = "Marshal: the run ended or the operator revoked this destination; nothing to grant"
	extremeReadReason           = "Marshal: reads outside the project apply automatically"
	extremeAwaitMarshalActivity = "Waiting for the Marshal to decide (no operator popup in Marshal): "
	extremeRunActionReason      = "Marshal: plan approval and delivery apply automatically under Marshal authority; no operator popup"
)

// automaticRunActions are run actions MARSHAL raises itself (an operator's
// /marshal approve, a runtime delivery confirmation after approval) that the
// private edition applies without an operator popup. They stay bound to the
// exact plan snapshot recorded in runProposals.
var automaticRunActions = map[string]bool{"approve": true, "amend-approve": true, "deliver": true}

// routeAutomaticRunAction applies a runtime- or operator-raised run action.
// Model proposals keep their own provenance path; foreign proposals still go
// to the Marshal.
func (w *Workspace) routeAutomaticRunAction(ctx context.Context, req permission.Request, provider string) bool {
	if req.Kind != "marshal-command" || req.RunID == "" || req.SourceLaunch != "" || req.SourceOccurrence != "" {
		return false
	}
	if req.ProposalID != "" && !strings.HasPrefix(req.ProposalID, "marshal-delivery-") {
		return false
	}
	w.permissions.mu.Lock()
	p, ok := w.permissions.runProposals[req.Key()]
	w.permissions.mu.Unlock()
	if !ok || !automaticRunActions[p.action] || p.command() != req.Object {
		return false
	}
	id := req.ProposalID
	if id == "" {
		id = "run-action:" + req.RunID
	}
	w.finishBuildDecisionAs(ctx, req, id, true, extremeRunActionReason, provider, marshalRuntimeDecider)
	return true
}

// automaticWorkKinds are the request kinds this edition applies itself.
var automaticWorkKinds = map[string]bool{"network": true, "read": true}

// routeAutomaticWork applies work decisions that never need anyone in this
// edition: network egress for a live run and reads of any folder. They go
// through the ordinary decision path, so PERMISSION_DECIDED evidence names
// the runtime as the deciding actor.
func (w *Workspace) routeAutomaticWork(ctx context.Context, req permission.Request) bool {
	if !automaticWorkKinds[req.Kind] {
		return false
	}
	switch req.Kind {
	case "network":
		if w.runtime == nil {
			return false
		}
		provider, automatic := w.runtime.AutomaticProviderEgressRequest(ctx, req.RunID, req.Object)
		if !automatic {
			provider, automatic = w.runtime.AutomaticOpenEgressRequest(ctx, req.RunID, req.Object)
		}
		if automatic {
			w.finishBuildDecisionAs(ctx, req, "egress:"+req.RunID, true, extremeNetworkReason, provider, marshalRuntimeDecider)
		} else {
			w.finishBuildDecisionAs(ctx, req, "egress:"+req.RunID, false, extremeNetworkClosedReason, provider, marshalRuntimeDecider)
		}
		return true
	case "read":
		id := req.ProposalID
		if id == "" {
			id = "read:" + req.Object
		}
		w.finishBuildDecisionAs(ctx, req, id, true, extremeReadReason, "", marshalRuntimeDecider)
		return true
	}
	return false
}

// awaitMarshalDecision keeps a cross-actor decision (another author's memory,
// a worker or local proposal, a reserved merge) queued for the Marshal while
// no Marshal chat is live, instead of opening an operator popup that a stray
// key would deny. It is delivered as soon as the Marshal is back.
func (w *Workspace) awaitMarshalDecision(req permission.Request) bool {
	if req.Kind != "memory" && req.Kind != "marshal-command" {
		return false
	}
	w.permissions.mu.Lock()
	if w.permissions.build.awaiting == nil {
		w.permissions.build.awaiting = map[string]bool{}
	}
	first := !w.permissions.build.awaiting[req.Key()]
	w.permissions.build.awaiting[req.Key()] = true
	w.permissions.queue.Add(req)
	w.permissions.mu.Unlock()
	if first {
		w.RecordActivity(extremeAwaitMarshalActivity + req.Kind + " " + req.Object)
	}
	return true
}

func (w *Workspace) stopAwaitingMarshal(req permission.Request) {
	w.permissions.mu.Lock()
	delete(w.permissions.build.awaiting, req.Key())
	w.permissions.mu.Unlock()
}
