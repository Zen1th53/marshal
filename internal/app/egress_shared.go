package app

import (
	"context"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/netpolicy"
)

// The randomized private proxy socket is a run incarnation and a liveness
// witness, not a command channel. Historical decisions never reopen a scope.
func egressScopeLive(socket string) bool {
	if socket == "" {
		return false
	}
	conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func projectEgressScopes(history []events.Event) (map[string]*runEgress, error) {
	scopes := map[string]*runEgress{}
	for _, event := range history {
		socket, _ := event.Data["scope_socket"].(string)
		if socket == "" {
			continue
		}
		if event.Type == events.EventTypeNetworkEgressRequested && event.Data["source"] == "run scope" {
			var defaults []string
			values, _ := event.Data["allowed_endpoints"].([]any)
			for _, value := range values {
				if endpoint, ok := value.(string); ok {
					defaults = append(defaults, endpoint)
				}
			}
			allowlist, err := netpolicy.NewRunAllowlist(defaults)
			if err != nil {
				return nil, err
			}
			provider, _ := event.Data["provider"].(string)
			parent, _ := event.Data["parent_run_id"].(string)
			scopes[event.RunID] = &runEgress{id: event.RunID, parent: parent, task: event.TaskID, worker: event.Subject, provider: provider, socket: socket, allowlist: allowlist, pending: map[string]bool{}}
			continue
		}
		scope := scopes[event.RunID]
		if scope == nil || scope.socket != socket {
			continue
		}
		endpoint, _ := event.Data["endpoint"].(string)
		switch event.Type {
		case events.EventTypeNetworkEgressNotification:
			scope.pending[endpoint] = true
		case events.EventTypeNetworkEgressGranted, events.EventTypeNetworkEgressRevoked:
			if event.Data["source"] != "operator command" {
				continue
			}
			if err := scope.allowlist.Set(endpoint, event.Type == events.EventTypeNetworkEgressGranted, func(string) error { return nil }); err != nil {
				return nil, err
			}
			normalized, _ := netpolicy.Endpoint(endpoint)
			delete(scope.pending, normalized)
		}
	}
	return scopes, nil
}

func (r *Runtime) sharedEgressRun(ctx context.Context, runID string) (*runEgress, error) {
	history, err := r.store.EgressControlEvents(ctx, runID, 0)
	if err != nil {
		return nil, err
	}
	scopes, err := projectEgressScopes(history)
	if err != nil {
		return nil, err
	}
	scope := scopes[runID]
	if scope == nil || !egressScopeLive(scope.socket) {
		return nil, fmt.Errorf("%w: no active egress run %s", model.ErrNotFound, runID)
	}
	return scope, nil
}

func (r *Runtime) sharedEgressStatus(ctx context.Context) ([]EgressStatus, error) {
	if r.store == nil {
		return nil, model.ErrUnavailable
	}
	history, err := r.store.EgressControlEvents(ctx, "", 0)
	if err != nil {
		return nil, err
	}
	scopes, err := projectEgressScopes(history)
	if err != nil {
		return nil, err
	}
	var rows []EgressStatus
	for _, scope := range scopes {
		if !egressScopeLive(scope.socket) {
			continue
		}
		row := EgressStatus{RunID: scope.id, ParentRunID: scope.parent, TaskID: scope.task, Worker: scope.worker, Provider: scope.provider, Allowed: scope.allowlist.Endpoints()}
		for endpoint := range scope.pending {
			row.Pending = append(row.Pending, endpoint)
		}
		sort.Strings(row.Allowed)
		sort.Strings(row.Pending)
		rows = append(rows, row)
	}
	return rows, nil
}

// OperatorEgressStatus merges live scopes in this runtime with scopes published
// by other runtimes sharing the project store. Store failures are explicit.
func (r *Runtime) OperatorEgressStatus(ctx context.Context) ([]EgressStatus, error) {
	rows, err := r.sharedEgressStatus(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.RunID] = true
	}
	for _, row := range r.EgressStatus() {
		if !seen[row.RunID] {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].RunID < rows[j].RunID })
	return rows, nil
}

type storedRunEgress struct {
	runtime *Runtime
	scope   *runEgress
}

// Read committed decisions on every evaluation, including the proxy's final
// check before dialing. A read error refuses access, including provider defaults.
func (e *storedRunEgress) Evaluate(ctx context.Context, request netpolicy.Request) (netpolicy.Decision, error) {
	history, err := e.runtime.store.EgressControlEvents(ctx, e.scope.id, 0)
	if err != nil {
		return netpolicy.Decision{Reason: netpolicy.ReasonDenied}, err
	}
	scopes, err := projectEgressScopes(history)
	if err != nil {
		return netpolicy.Decision{Reason: netpolicy.ReasonDenied}, err
	}
	scope := scopes[e.scope.id]
	if scope == nil || scope.socket != e.scope.socket {
		return netpolicy.Decision{Reason: netpolicy.ReasonDenied}, model.ErrUnavailable
	}
	return scope.allowlist.Evaluate(ctx, request)
}

// New requests observe revocation synchronously. Existing connections are closed
// within one polling interval. Replay every revoke, even if a later allow arrives
// before the poll, so an old tunnel cannot survive revoke followed by regrant.
func (r *Runtime) watchEgressRevocations(ctx context.Context, scope *runEgress) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var after events.Sequence
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			history, err := r.store.EgressControlEvents(ctx, scope.id, after)
			if err != nil {
				if ctx.Err() == nil {
					_ = scope.proxy.Close()
				}
				return
			}
			for _, event := range history {
				after = event.Sequence
				if event.Type == events.EventTypeNetworkEgressRevoked && event.Data["source"] == "operator command" && event.Data["scope_socket"] == scope.socket {
					endpoint, _ := event.Data["endpoint"].(string)
					scope.proxy.CloseEndpoint(endpoint)
				}
			}
		}
	}
}
