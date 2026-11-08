package tui

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/model"
)

type ultraRequestServiceProvider interface {
	WorkspaceEntitlementRequests() *app.ULTRARequestService
}
type ultraRequestActionAuthority interface {
	PersistedEntitlementRequestReader
	EntitlementRequestTarget(context.Context) (app.EntitlementRequestTarget, error)
	SubmitEntitlementRequest(context.Context, app.CommandEnvelope, app.EntitlementRequestTarget) (cloud.RequestObservation, error)
}

func (a *runtimeControlAuthority) ultraRequestService() (*app.ULTRARequestService, error) {
	if a == nil || a.runtime == nil {
		return nil, errNoRuntime
	}
	p, ok := any(a.runtime).(ultraRequestServiceProvider)
	if !ok || p.WorkspaceEntitlementRequests() == nil {
		if editionExtreme {
			return nil, fmt.Errorf("durable entitlement request service unavailable: ULTRA is local in this build, so there is no cloud entitlement to request")
		}
		return nil, fmt.Errorf("durable entitlement request service unavailable")
	}
	return p.WorkspaceEntitlementRequests(), nil
}

func (a *runtimeControlAuthority) PersistedEntitlementRequest(ctx context.Context) (cloud.RequestObservation, bool, error) {
	s, err := a.ultraRequestService()
	if err != nil {
		return cloud.RequestObservation{}, false, err
	}
	return s.Read(ctx)
}
func (a *runtimeControlAuthority) EntitlementRequest(ctx context.Context) (EntitlementRequestObservation, error) {
	o, found, err := a.PersistedEntitlementRequest(ctx)
	return requestPresentation(o, found), err
}
func (a *runtimeControlAuthority) EntitlementRequestTarget(ctx context.Context) (app.EntitlementRequestTarget, error) {
	s, err := a.ultraRequestService()
	if err != nil {
		return app.EntitlementRequestTarget{}, err
	}
	return s.Target(ctx)
}
func (a *runtimeControlAuthority) SubmitEntitlementRequest(ctx context.Context, e app.CommandEnvelope, target app.EntitlementRequestTarget) (cloud.RequestObservation, error) {
	if a == nil || a.localControl == nil || e.SessionID != a.sessionID {
		return cloud.RequestObservation{}, ErrNoBinding
	}
	s, err := a.ultraRequestService()
	if err != nil {
		return cloud.RequestObservation{}, err
	}
	return s.Submit(a.localControl.Context(ctx), e, target)
}

func ultraRequestActionBindings(s *ControlSource) map[ActionID]Binding {
	id := ActionID("CTUI-0035")
	const source = "internal/app/ultra_requests.go CommandRequestEntitlement"
	return map[ActionID]Binding{id: {Action: id, Title: "Record and submit ULTRA entitlement request", Safety: SafetyGoverned,
		Prepare: func(ctx context.Context) (Target, error) {
			if s == nil {
				return Target{}, ErrNoBinding
			}
			a, ok := s.Authority.(ultraRequestActionAuthority)
			if !ok {
				return Target{}, ErrNoBinding
			}
			target, err := a.EntitlementRequestTarget(ctx)
			if err != nil {
				return Target{}, err
			}
			return entitlementRequestTarget(target), nil
		},
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			if s == nil {
				return refusal("request authority unavailable", ErrNoBinding, req.Target, source)
			}
			a, ok := s.Authority.(ultraRequestActionAuthority)
			if !ok {
				return refusal("request authority unavailable", ErrNoBinding, req.Target, source)
			}
			if req.ProjectID != s.ProjectID || req.SessionID != s.SessionID || req.Target.Kind != "entitlement-request" {
				return refusal("request target changed", ErrStaleTarget, req.Target, source)
			}
			target, err := a.EntitlementRequestTarget(ctx)
			if err != nil {
				return refusal("request target unavailable", err, req.Target, source)
			}
			// Preserve the reviewed generation. The canonical service arbitrates
			// replay/CAS; a fresh read must never silently rebind a confirmation.
			target.ExpectedGeneration = req.Target.Revision
			bound := entitlementRequestTarget(target)
			if bound.ID != req.Target.ID || bound.Digest != req.Target.Digest {
				return refusal("request target changed", ErrStaleTarget, req.Target, source)
			}
			result, callErr := a.SubmitEntitlementRequest(ctx, app.CommandEnvelope{ProjectID: req.ProjectID, SessionID: req.SessionID, TargetID: bound.ID, ExpectedVersion: bound.Revision, IdempotencyKey: req.IdempotencyKey}, target)
			proof, found, readErr := a.PersistedEntitlementRequest(ctx)
			if readErr != nil || !found || proof.LocalAttemptID == "" || proof.LocalAttemptID != result.LocalAttemptID || proof.Generation != result.Generation {
				return unproven(req.Target, req.Target.ID, fmt.Errorf("request durable proof unavailable: %v", readErr))
			}
			detail := fmt.Sprintf("Local attempt %s recorded; observed request state %s; server request ID UNKNOWN", proof.LocalAttemptID, proof.State)
			if proof.ServerRequestID != "" {
				detail = fmt.Sprintf("Local attempt %s recorded; observed server request %s: %s", proof.LocalAttemptID, proof.ServerRequestID, proof.State)
			}
			if callErr != nil || proof.State == "sending" || proof.State == "unknown" {
				return Outcome{Verdict: VerdictUnknown, Detail: "Attempt recorded; server outcome UNKNOWN; a separate signed lease is required", Target: bound, Proof: Unknown(detail, source)}, nil
			}
			return Outcome{Verdict: VerdictPass, Detail: detail + "; this observation grants no ULTRA lease", Target: bound, Proof: Known(detail, source)}, nil
		},
	}}
}

func entitlementRequestTarget(t app.EntitlementRequestTarget) Target {
	return Target{Kind: "entitlement-request", ID: model.SessionRecoveryDigest(t.Key), Revision: t.ExpectedGeneration, Digest: model.SessionRecoveryDigest(t), Scope: t.Key.Endpoint, Summary: fmt.Sprintf("Request ULTRA for installation %s, session %s (local attempt tracking only)", t.Key.InstallationID, t.Key.SessionID)}
}
