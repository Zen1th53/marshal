package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

type sessionRecoveryAuthority interface {
	RecoveryContext(context.Context, string, int64) (model.SessionRecovery, error)
	AdoptRecoveryContext(context.Context, app.CommandEnvelope, string, string) (model.SessionRecovery, error)
}

// The integrator supplies this optional method on Runtime, backed by trusted
// workspace composition. Its absence deliberately leaves adoption unavailable.
type sessionRecoveryHookProvider interface {
	WorkspaceRecoveryHooks() app.SessionRecoveryHooks
}

func (a *runtimeControlAuthority) RecoveryContext(ctx context.Context, id string, generation int64) (model.SessionRecovery, error) {
	if a == nil || a.runtime == nil {
		return model.SessionRecovery{}, errNoRuntime
	}
	return a.runtime.WorkspaceRecovery(app.SessionRecoveryHooks{}).Get(ctx, id, generation)
}

func (a *runtimeControlAuthority) AdoptRecoveryContext(ctx context.Context, e app.CommandEnvelope, digest, destination string) (model.SessionRecovery, error) {
	if a == nil || a.runtime == nil || a.localControl == nil || destination != a.sessionID || e.ProjectID != a.runtime.ProjectIdentity() {
		return model.SessionRecovery{}, ErrNoBinding
	}
	p, ok := any(a.runtime).(sessionRecoveryHookProvider)
	if !ok {
		return model.SessionRecovery{}, fmt.Errorf("trusted workspace recovery validation is unavailable")
	}
	return a.runtime.WorkspaceRecovery(p.WorkspaceRecoveryHooks()).Adopt(a.localControl.Context(ctx), e, digest, destination)
}

func recoveryActionBindings(s *ControlSource) map[ActionID]Binding {
	id := ActionID("CTUI-0073")
	return map[ActionID]Binding{id: {Action: id, Title: "Adopt interrupted workspace context", Safety: SafetyPlain,
		Inputs: []InputSpec{{Key: "recovery_id", Label: "Exact durable workspace recovery ID", Required: true}},
		PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
			if s == nil {
				return Target{}, ErrNoBinding
			}
			a, ok := s.Authority.(sessionRecoveryAuthority)
			if !ok {
				return Target{}, ErrNoBinding
			}
			id := strings.TrimSpace(req.Inputs["recovery_id"])
			if id == "" {
				return Target{}, model.ErrInvalid
			}
			r, err := a.RecoveryContext(ctx, id, 0)
			if err != nil {
				return Target{}, err
			}
			if r.State != "recoverable" || r.Binding.ProjectID != s.ProjectID {
				return Target{}, ErrStaleTarget
			}
			return recoveryTarget(r), nil
		},
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			const source = "internal/app/session_recovery.go SessionRecoveryService.Adopt"
			if s == nil {
				return refusal("recovery authority unavailable", ErrNoBinding, req.Target, source)
			}
			a, ok := s.Authority.(sessionRecoveryAuthority)
			if !ok {
				return refusal("recovery authority unavailable", ErrNoBinding, req.Target, source)
			}
			if req.ProjectID != s.ProjectID || req.SessionID != s.SessionID || req.Target.Kind != "workspace-recovery" || req.Target.ID != strings.TrimSpace(req.Inputs["recovery_id"]) {
				return refusal("recovery target changed", ErrStaleTarget, req.Target, source)
			}
			r, err := a.RecoveryContext(ctx, req.Target.ID, req.Target.Revision)
			if err != nil {
				return refusal("recovery context unavailable", err, req.Target, source)
			}
			if r.Digest != req.Target.Digest {
				return refusal("recovery digest changed", ErrStaleTarget, req.Target, source)
			}
			adopted, err := a.AdoptRecoveryContext(ctx, app.CommandEnvelope{ProjectID: req.ProjectID, SessionID: r.SourceSessionID, TargetID: r.ID, ExpectedVersion: r.Generation, IdempotencyKey: req.IdempotencyKey}, r.Digest, req.SessionID)
			if err != nil {
				return refusal("workspace recovery refused", err, req.Target, source)
			}
			proof, err := a.RecoveryContext(ctx, adopted.ID, adopted.Generation)
			if err != nil || proof.State != "adopted" || proof.AdoptedSessionID != req.SessionID || proof.Binding != r.Binding {
				return unproven(req.Target, r.ID, fmt.Errorf("durable adoption proof unavailable: %v", err))
			}
			return Outcome{Verdict: VerdictPass, Detail: "Workspace references adopted; provider execution requires a separate reviewed action", Target: recoveryTarget(proof), Proof: Known(fmt.Sprintf("adopted generation %d into session %s", proof.Generation, proof.AdoptedSessionID), source), Evidence: proof.Digest}, nil
		},
	}}
}

func recoveryTarget(r model.SessionRecovery) Target {
	return Target{Kind: "workspace-recovery", ID: r.ID, Revision: r.Generation, Digest: r.Digest, Scope: r.Binding.ProjectID, Summary: fmt.Sprintf("Workspace %s; Goal %s@%d; %d exact memory references", r.SourceSessionID, r.Binding.GoalID, r.Binding.GoalRevision, len(r.Memory))}
}
