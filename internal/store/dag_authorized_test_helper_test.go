package store

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/dag"
	"github.com/Zen1th53/marshal/internal/events"
)

func newAuthorizedDAGEngine(backend dag.Backend) (*dag.Engine, error) {
	identity := dag.IdentityProviderFunc(func(context.Context) (dag.Identity, error) {
		return dag.Identity{SubjectID: "SUBJECT-TEST", SessionID: "SESSION-TEST", TaskID: "TASK-TEST", ChangeID: "CHANGE-TEST"}, nil
	})
	authorizer := dag.AuthorizerFunc(func(_ context.Context, request dag.AuthorizationRequest) (dag.AuthorizationDecision, error) {
		return dag.AuthorizationDecision{
			Allowed: true, Identity: request.Identity, RequestID: request.RequestID, Action: request.Action,
			Resource: request.Resource, ExpectedState: request.ExpectedState, TargetState: request.TargetState,
			PolicyDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			FreshUntil:   time.Now().Add(time.Hour),
		}, nil
	})
	return dag.NewAuditedEngine(backend, identity, authorizer, dag.FreshnessValidatorFunc(func(context.Context, dag.AuthorizationRequest, dag.AuthorizationDecision) error { return nil }), dag.EventSinkFunc(func(context.Context, events.Event) (events.Event, error) { return events.Event{}, nil }))
}
