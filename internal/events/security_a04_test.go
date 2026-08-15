package events

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestT43A04PassEventRequiresEvidence(t *testing.T) {
	event := Event{
		ID:             "EVENT-A04-PASS",
		Type:           "policytest.case.passed",
		Subject:        "SUBJECT-A04",
		TaskID:         "TASK-A04",
		IdempotencyKey: "REQ-A04-PASS",
	}
	if err := event.Validate(); !errors.Is(err, ErrEvidenceRequired) {
		t.Fatalf("Validate error=%v, want evidence required", err)
	}
}

func TestT43A04ProducerFailsClosedWithoutAuthority(t *testing.T) {
	store := &a02EngineStore{stored: Event{ID: "EVENT-A04-AUTH", Sequence: 1, Type: "events.appended", Subject: "SUBJECT-A04", At: time.Now().UTC(), IdempotencyKey: "REQ-A04-AUTH"}}
	bus := &a02EngineBus{store: store}
	engine, err := NewEngine(store, bus)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Process(context.Background(), Event{ID: "EVENT-A04-AUTH", Type: "events.appended", Subject: "SUBJECT-A04", IdempotencyKey: "REQ-A04-AUTH"})
	if !errors.Is(err, ErrAuthorizationUnavailable) {
		t.Fatalf("error=%v, want authorization unavailable", err)
	}
	if result.State != StateProduced || store.called || bus.called {
		t.Fatalf("result=%+v store=%v bus=%v", result, store.called, bus.called)
	}
}

type a04EventIdentityProvider struct {
	identity ProducerIdentity
	err      error
}

func (p a04EventIdentityProvider) Identity(context.Context) (ProducerIdentity, error) {
	return p.identity, p.err
}

type a04EventAuthorizer struct {
	decide func(AuthorizationRequest) AuthorizationDecision
	err    error
}

func (a a04EventAuthorizer) Authorize(_ context.Context, r AuthorizationRequest) (AuthorizationDecision, error) {
	if a.err != nil {
		return AuthorizationDecision{}, a.err
	}
	return a.decide(r), nil
}

func a04EventIdentity() ProducerIdentity {
	return ProducerIdentity{SubjectID: "SUBJECT-A04", SessionID: "SESSION-A04", TaskID: "TASK-A04", RunID: "RUN-A04", ChangeID: "CHANGE-A04"}
}
func a04EventAllow(r AuthorizationRequest) AuthorizationDecision {
	return AuthorizationDecision{Allowed: true, Identity: r.Identity, Action: r.Action, EventID: r.EventID, Type: r.Type, TaskID: r.TaskID, RunID: r.RunID, ResourceID: r.ResourceID, EvidenceID: r.EvidenceID, IdempotencyKey: r.IdempotencyKey, PolicyDigest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", FreshUntil: time.Now().Add(time.Hour)}
}
func a04Fresh() FreshnessValidator {
	return FreshnessValidatorFunc(func(context.Context, AuthorizationRequest, AuthorizationDecision) error { return nil })
}

func TestT43A04PayloadCannotForgeCanonicalProducerIdentity(t *testing.T) {
	store := &a02EngineStore{}
	bus := &a02EngineBus{store: store}
	engine, err := NewAuthorizedEngine(store, bus, a04EventIdentityProvider{identity: a04EventIdentity()}, a04EventAuthorizer{decide: a04EventAllow}, a04Fresh())
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Process(context.Background(), Event{ID: "EVENT-A04-SPOOF", Type: "events.appended", Subject: "ADMIN", TaskID: "TASK-A04", RunID: "RUN-A04", IdempotencyKey: "REQ-A04-SPOOF"})
	if !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("error=%v, want denied", err)
	}
	if store.called || bus.called {
		t.Fatalf("store=%v bus=%v", store.called, bus.called)
	}
}

func TestT43A04ForeignTaskOrRunRejectedBeforeStore(t *testing.T) {
	for _, event := range []Event{
		{ID: "EVENT-A04-FOREIGN-TASK", Type: "events.appended", Subject: "SUBJECT-A04", TaskID: "TASK-FOREIGN", RunID: "RUN-A04", IdempotencyKey: "REQ-A04-FOREIGN-TASK"},
		{ID: "EVENT-A04-FOREIGN-RUN", Type: "events.appended", Subject: "SUBJECT-A04", TaskID: "TASK-A04", RunID: "RUN-FOREIGN", IdempotencyKey: "REQ-A04-FOREIGN-RUN"},
	} {
		store := &a02EngineStore{}
		bus := &a02EngineBus{store: store}
		engine, _ := NewAuthorizedEngine(store, bus, a04EventIdentityProvider{identity: a04EventIdentity()}, a04EventAuthorizer{decide: a04EventAllow}, a04Fresh())
		if _, err := engine.Process(context.Background(), event); !errors.Is(err, ErrAuthorizationDenied) {
			t.Fatalf("event=%s error=%v", event.ID, err)
		}
		if store.called {
			t.Fatalf("event=%s reached store", event.ID)
		}
	}
}

func TestT43A04DecisionBindingAndFreshnessFailClosed(t *testing.T) {
	event := Event{ID: "EVENT-A04-BIND", Type: "events.appended", Subject: "SUBJECT-A04", TaskID: "TASK-A04", RunID: "RUN-A04", IdempotencyKey: "REQ-A04-BIND"}
	cases := []struct {
		name      string
		decide    func(AuthorizationRequest) AuthorizationDecision
		freshness FreshnessValidator
		want      error
	}{
		{name: "wrong-event", decide: func(r AuthorizationRequest) AuthorizationDecision {
			d := a04EventAllow(r)
			d.EventID = "EVENT-OTHER"
			return d
		}, freshness: a04Fresh(), want: ErrAuthorizationDenied},
		{name: "expired", decide: func(r AuthorizationRequest) AuthorizationDecision {
			d := a04EventAllow(r)
			d.FreshUntil = time.Now().Add(-time.Second)
			return d
		}, freshness: a04Fresh(), want: ErrAuthorizationStale},
		{name: "canonical-stale", decide: a04EventAllow, freshness: FreshnessValidatorFunc(func(context.Context, AuthorizationRequest, AuthorizationDecision) error {
			return errors.New("stale policy")
		}), want: ErrAuthorizationStale},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			store := &a02EngineStore{}
			bus := &a02EngineBus{store: store}
			engine, _ := NewAuthorizedEngine(store, bus, a04EventIdentityProvider{identity: a04EventIdentity()}, a04EventAuthorizer{decide: tt.decide}, tt.freshness)
			if _, err := engine.Process(context.Background(), event); !errors.Is(err, tt.want) {
				t.Fatalf("error=%v want=%v", err, tt.want)
			}
			if store.called {
				t.Fatal("stale/mismatched decision reached durable store")
			}
		})
	}
}

func TestT43A04ValidAuthorityPersistsBeforePublish(t *testing.T) {
	at := time.Date(2026, 8, 15, 15, 0, 0, 0, time.UTC)
	canonical := Event{ID: "EVENT-A04-ALLOW", Sequence: 31, Type: "events.appended", Subject: "SUBJECT-A04", TaskID: "TASK-A04", RunID: "RUN-A04", At: at, IdempotencyKey: "REQ-A04-ALLOW"}
	store := &a02EngineStore{stored: canonical}
	bus := &a02EngineBus{store: store}
	engine, _ := NewAuthorizedEngine(store, bus, a04EventIdentityProvider{identity: a04EventIdentity()}, a04EventAuthorizer{decide: a04EventAllow}, a04Fresh())
	result, err := engine.Process(context.Background(), Event{ID: "EVENT-A04-ALLOW", Type: "events.appended", Subject: "SUBJECT-A04", TaskID: "TASK-A04", RunID: "RUN-A04", IdempotencyKey: "REQ-A04-ALLOW"})
	if err != nil {
		t.Fatal(err)
	}
	if !store.called || !bus.called || result.State != StatePublished || result.Event.Sequence != 31 {
		t.Fatalf("result=%+v store=%v bus=%v", result, store.called, bus.called)
	}
}

func TestT43A04AuthorityErrorDoesNotLeakSecret(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T43_A04_91ac"
	store := &a02EngineStore{}
	bus := &a02EngineBus{store: store}
	engine, _ := NewAuthorizedEngine(store, bus, a04EventIdentityProvider{identity: a04EventIdentity()}, a04EventAuthorizer{err: errors.New(marker)}, a04Fresh())
	_, err := engine.Process(context.Background(), Event{ID: "EVENT-A04-SECRET", Type: "events.appended", Subject: "SUBJECT-A04", TaskID: "TASK-A04", RunID: "RUN-A04", IdempotencyKey: "REQ-A04-SECRET"})
	if !errors.Is(err, ErrAuthorizationUnavailable) {
		t.Fatalf("error=%v", err)
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("secret leaked: %q", err)
	}
	if store.called {
		t.Fatal("authorization error reached store")
	}
}
