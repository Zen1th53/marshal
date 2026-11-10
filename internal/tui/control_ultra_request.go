package tui

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/cloud"
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
