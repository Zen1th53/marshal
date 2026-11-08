package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/netpolicy"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/sandbox"
)

// HasCredentialGrant replays committed project operator decisions. Failure to
// read evidence is denial; grants are never inferred from messages or config.
func (r *Runtime) HasCredentialGrant(ctx context.Context, provider string) bool {
	if r == nil || r.store == nil {
		return false
	}
	allowed, _, _, err := r.standingCredentialDecision(ctx, provider)
	return err == nil && allowed
}

// standingCredentialDecision replays the latest standing decision and the
// surface that recorded it. The latest decision always wins.
func (r *Runtime) standingCredentialDecision(ctx context.Context, provider string) (allowed, decided bool, source string, err error) {
	records, err := r.store.ListEvents(ctx)
	if err != nil {
		return false, false, "", err
	}
	for _, e := range records {
		if e.Type != "PERMISSION_DECIDED" || e.ProjectID != r.ProjectID() || e.Data["kind"] != "credential" || e.Data["object"] != provider {
			continue
		}
		allowed, _ = e.Data["allow"].(bool)
		source, _ = e.Data["source"].(string)
		decided = true
	}
	return allowed, decided, source, nil
}

// explicitlyRevokedCredential reports whether the latest standing decision is
// a binding operator revoke (Extreme keeps honoring only that denial).
func (r *Runtime) explicitlyRevokedCredential(ctx context.Context, provider string) (bool, error) {
	allowed, decided, source, err := r.standingCredentialDecision(ctx, provider)
	return decided && !allowed && bindingCredentialDenial(source), err
}

const (
	credentialRuntimeDecider = "marshal-runtime"
	credentialRuntimeReason  = "Marshal: provider credential applies automatically"
)

// recordAutomaticCredentialDecision is the Extreme runtime's own allow for a
// registered provider's credential request: the same PERMISSION_DECIDED
// evidence an answered request leaves (approval binding and bounded scope
// included), naming the runtime as the decider.
func (r *Runtime) recordAutomaticCredentialDecision(ctx context.Context, req permission.Request, lease *boundCredentialLease) (string, error) {
	if !automaticCredentialDecision || !credentialProvider(req.Object) {
		return "", errors.New("automatic credential decision unavailable")
	}
	id, err := model.NewID("EVENT-PERMISSION-")
	if err != nil {
		return "", err
	}
	data := map[string]any{"kind": req.Kind, "object": req.Object, "scope": req.Scope, "requester": req.Who, "request_reason": req.Reason, "reason": credentialRuntimeReason, "decision_reason": credentialRuntimeReason, "allow": true, "actor": credentialRuntimeDecider, "source": credentialRuntimeDecider, "decided_by": credentialRuntimeDecider, "runtime_instance": r.runtimeInstanceID, "run_id": req.RunID, "task_id": req.TaskID, "approval_binding": req.ApprovalBinding}
	if lease != nil {
		scope, _ := json.Marshal(lease.scope)
		data["bound_scope"] = string(scope)
		data["bound_expires"] = lease.expires.Format(time.RFC3339Nano)
	}
	r.permissionMu.Lock()
	defer r.permissionMu.Unlock()
	return id, r.store.AppendEvent(ctx, nil, model.Event{ID: id, Type: "PERMISSION_DECIDED", ProjectID: r.ProjectID(), Timestamp: time.Now().UTC(), Data: data})
}

func credentialProvider(provider string) bool {
	switch provider {
	case "codex", "claude", "opencode":
		return true
	}
	return privateAGYCredential(provider)
}

func (r *Runtime) providerBroker(ctx context.Context, provider, model string) (*netpolicy.CredentialBroker, error) {
	// OpenCode's free/local providers do not require operator credentials.
	if provider == "opencode" && !netpolicy.CredentialProfileSupported(provider, model) {
		return nil, nil
	}
	if _, hasBound := r.boundCredentialForContext(ctx, provider); hasBound {
		// Only VerifyMarshalWorker and validated Process05 attempts mint this
		// context. Ordinary Runtime.Run remains on the standing project grant;
		// a historical bounded decision cannot authorize that path.
		scope, valid := r.boundScopeFromContext(ctx, provider, model)
		if !valid {
			return nil, netpolicy.ErrCredentialBroker
		}
		if _, err := r.waitBoundCredential(ctx, scope); err != nil {
			return nil, fmt.Errorf("%w: bounded %s credential permission unavailable: %w", netpolicy.ErrCredentialBroker, provider, err)
		}
		return netpolicy.LoadCredentialBroker(provider, model)
	}
	granted, err := r.waitCredentialGrant(ctx, provider, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("%w: %s credential permission unavailable: %w", netpolicy.ErrCredentialBroker, provider, err)
	}
	if !granted {
		return nil, fmt.Errorf("%w: %s credential use denied (operator denial or permission timeout)", netpolicy.ErrCredentialBroker, provider)
	}
	return netpolicy.LoadCredentialBroker(provider, model)
}

func (r *Runtime) sandboxBroker(socket, home string) ([]string, error) {
	r.egressMu.Lock()
	var broker *netpolicy.CredentialBroker
	var scope *runEgress
	for _, candidate := range r.egressRuns {
		if candidate.socket == socket {
			broker = candidate.broker
			scope = candidate
			break
		}
	}
	r.egressMu.Unlock()
	if broker == nil {
		return nil, nil
	}
	if scope == nil || !r.credentialAllowedForEgress(context.Background(), scope) {
		return nil, netpolicy.ErrCredentialBroker
	}
	roots, err := sandbox.SystemRootBundle()
	if err != nil {
		return nil, err
	}
	return broker.Prepare(home, "/home/marshal", roots)
}

// Wait only when a decision surface exists. Persistent denial remains denial;
// an undecided request cannot fail a governed run ahead of its operator popup.
func (r *Runtime) waitCredentialGrant(ctx context.Context, provider string, timeout time.Duration) (bool, error) {
	decision := func() (bool, bool, error) {
		allowed, decided, _, err := r.standingCredentialDecision(ctx, provider)
		return allowed, decided, err
	}
	allowed, decided, source, err := r.standingCredentialDecision(ctx, provider)
	if err != nil {
		return false, err
	}
	if automaticCredentialDecision && !allowed && !(decided && bindingCredentialDenial(source)) && credentialProvider(provider) {
		// No sink or popup is consulted. A later automatic grant supersedes a
		// popup/timeout denial; only an explicit operator revoke still holds.
		req := permission.Request{Kind: "credential", Object: provider, Scope: "this project, until revoked", Who: "MARSHAL", Reason: "governed provider work needs its credential broker"}
		if _, err := r.recordAutomaticCredentialDecision(ctx, req, nil); err != nil {
			return false, err
		}
		return r.HasCredentialGrant(ctx, provider), nil
	}
	if decided {
		return allowed, nil
	}
	r.permissionMu.Lock()
	sink := r.permissionSink
	r.permissionMu.Unlock()
	if sink == nil {
		return false, errors.New("no operator decision surface")
	}
	sink(permission.Request{Kind: "credential", Object: provider, Scope: "this project, until revoked", Who: "MARSHAL"})
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if allowed, decided, err := decision(); err != nil || decided {
			return allowed, err
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-timer.C:
			return false, nil
		case <-ticker.C:
		}
	}
}
