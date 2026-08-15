package events

import (
	"context"
	"testing"
)

func TestT43A09MetricsAreBoundedDetachedAndDoNotInfluenceOrdering(t *testing.T) {
	store := &a02EngineStore{}
	bus := &a02EngineBus{store: store}
	engine, err := NewEngine(store, bus)
	if err != nil {
		t.Fatal(err)
	}
	// A09 metrics must observe service calls even when A04 authority is absent;
	// they cannot make an unauthorized call succeed.
	_, _ = engine.Process(context.Background(), Event{ID: "EVENT-A09", Type: "events.appended", Subject: "system", IdempotencyKey: "REQ-A09"})
	snap := engine.Metrics()
	if snap.Observations[MetricOperationProcess] != 1 {
		t.Fatalf("process observations=%d", snap.Observations[MetricOperationProcess])
	}
	snap.Observations[MetricOperationProcess] = 999
	if engine.Metrics().Observations[MetricOperationProcess] != 1 {
		t.Fatal("snapshot aliases recorder")
	}
	if store.called {
		t.Fatal("metrics bypassed authorization")
	}
}

func TestT43A09PublishOutcomeUsesClosedDimensions(t *testing.T) {
	recorder := NewMetricsRecorder()
	recorder.Observe(MetricOperationPublish, MetricOutcomeDropped, 1)
	recorder.Observe(MetricOperation("attacker-id"), MetricOutcome("secret-label"), 1)
	snap := recorder.Snapshot()
	if snap.Observations[MetricOperationPublish] != 1 || snap.Outcomes[MetricOutcomeDropped] != 1 {
		t.Fatalf("snapshot=%+v", snap)
	}
	if len(snap.Observations) != 1 || len(snap.Outcomes) != 1 {
		t.Fatal("unbounded dimensions recorded")
	}
}

func TestT43A09DeniedAndInvalidProcessUseClosedOutcomes(t *testing.T) {
	store := &a02EngineStore{}
	bus := &a02EngineBus{store: store}
	engine, err := NewAuthorizedEngine(
		store,
		bus,
		IdentityProviderFunc(func(context.Context) (ProducerIdentity, error) {
			return ProducerIdentity{SubjectID: "SUBJECT-A09", SessionID: "SESSION-A09", TaskID: "TASK-A09", RunID: "RUN-A09", ChangeID: "CHANGE-A09"}, nil
		}),
		AuthorizerFunc(func(_ context.Context, r AuthorizationRequest) (AuthorizationDecision, error) {
			return AuthorizationDecision{Allowed: false, Identity: r.Identity, Action: r.Action, EventID: r.EventID, Type: r.Type, TaskID: r.TaskID, RunID: r.RunID, ResourceID: r.ResourceID, EvidenceID: r.EvidenceID, IdempotencyKey: r.IdempotencyKey, PolicyDigest: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}, nil
		}),
		FreshnessValidatorFunc(func(context.Context, AuthorizationRequest, AuthorizationDecision) error { return nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = engine.Process(context.Background(), Event{ID: "EVENT-A09-DENY", Type: "events.appended", Subject: "SUBJECT-A09", TaskID: "TASK-A09", RunID: "RUN-A09", IdempotencyKey: "REQ-A09-DENY"})
	_, _ = engine.Process(context.Background(), Event{ID: "EVENT-A09-INVALID", Type: "not.registered", Subject: "SUBJECT-A09", TaskID: "TASK-A09", RunID: "RUN-A09", IdempotencyKey: "REQ-A09-INVALID"})
	snap := engine.Metrics()
	if snap.Outcomes[MetricOutcomeDenied] != 1 || snap.Outcomes[MetricOutcomeInvalid] != 1 {
		t.Fatalf("snapshot=%+v", snap)
	}
	if store.called {
		t.Fatal("metrics changed fail-closed behavior")
	}
}
