package events

import (
	"context"
	"time"
)

func newAuthorizedTestEngine(store Store, bus Bus) (*Engine, error) {
	identity := IdentityProviderFunc(func(context.Context) (ProducerIdentity, error) {
		return ProducerIdentity{SubjectID: "system", SessionID: "SESSION-TEST"}, nil
	})
	authorizer := AuthorizerFunc(func(_ context.Context, r AuthorizationRequest) (AuthorizationDecision, error) {
		return AuthorizationDecision{
			Allowed: true, Identity: r.Identity, Action: r.Action, EventID: r.EventID, Type: r.Type,
			TaskID: r.TaskID, RunID: r.RunID, ResourceID: r.ResourceID, EvidenceID: r.EvidenceID,
			IdempotencyKey: r.IdempotencyKey,
			PolicyDigest:   "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			FreshUntil:     time.Now().Add(time.Hour),
		}, nil
	})
	freshness := FreshnessValidatorFunc(func(context.Context, AuthorizationRequest, AuthorizationDecision) error { return nil })
	return NewAuthorizedEngine(store, bus, identity, authorizer, freshness)
}
