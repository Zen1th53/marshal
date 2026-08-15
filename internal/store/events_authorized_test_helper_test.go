package store

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
)

func newAuthorizedEventEngineForStoreTests(st events.Store, bus events.Bus) (*events.Engine, error) {
	identity := events.IdentityProviderFunc(func(context.Context) (events.ProducerIdentity, error) {
		return events.ProducerIdentity{SubjectID: "system", SessionID: "SESSION-TEST"}, nil
	})
	authorizer := events.AuthorizerFunc(func(_ context.Context, r events.AuthorizationRequest) (events.AuthorizationDecision, error) {
		return events.AuthorizationDecision{Allowed: true, Identity: r.Identity, Action: r.Action, EventID: r.EventID, Type: r.Type, TaskID: r.TaskID, RunID: r.RunID, ResourceID: r.ResourceID, EvidenceID: r.EvidenceID, IdempotencyKey: r.IdempotencyKey, PolicyDigest: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", FreshUntil: time.Now().Add(time.Hour)}, nil
	})
	freshness := events.FreshnessValidatorFunc(func(context.Context, events.AuthorizationRequest, events.AuthorizationDecision) error { return nil })
	return events.NewAuthorizedEngine(st, bus, identity, authorizer, freshness)
}
