package store

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
)

func t43A10Engine(t *testing.T, st *Store, bus events.Bus) *events.Engine {
	t.Helper()
	identity := events.ProducerIdentity{SubjectID: "SUBJECT-A10", SessionID: "SESSION-A10", ChangeID: "CHANGE-A10"}
	engine, err := events.NewAuthorizedEngine(
		st, bus,
		events.IdentityProviderFunc(func(context.Context) (events.ProducerIdentity, error) { return identity, nil }),
		events.AuthorizerFunc(func(_ context.Context, r events.AuthorizationRequest) (events.AuthorizationDecision, error) {
			return events.AuthorizationDecision{
				Allowed: true, Identity: r.Identity, Action: r.Action, EventID: r.EventID, Type: r.Type,
				TaskID: r.TaskID, RunID: r.RunID, ResourceID: r.ResourceID, EvidenceID: r.EvidenceID,
				IdempotencyKey: r.IdempotencyKey,
				PolicyDigest:   "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
				FreshUntil:     time.Now().Add(time.Hour),
			}, nil
		}),
		events.FreshnessValidatorFunc(func(context.Context, events.AuthorizationRequest, events.AuthorizationDecision) error { return nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func TestT43A10ReleaseFlowResumesAfterRestartAndRetry(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/events-a10.db"
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		t.Fatal(err)
	}
	engine := t43A10Engine(t, st, events.NewMemoryBus(2))
	inputs := []events.Event{
		{ID: "EVENT-A10-1", Type: "scheduler.lease.acquired", Subject: "SUBJECT-A10", IdempotencyKey: "REQ-A10-1"},
		{ID: "EVENT-A10-2", Type: "scheduler.lease.renewed", Subject: "SUBJECT-A10", IdempotencyKey: "REQ-A10-2"},
	}
	for _, input := range inputs {
		if _, err := engine.Append(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	engine = t43A10Engine(t, reopened, events.NewMemoryBus(2))
	missed, err := engine.Since(ctx, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(missed) != 1 || missed[0].ID != "EVENT-A10-2" || missed[0].Sequence != 2 {
		t.Fatalf("missed=%+v", missed)
	}
	retried, err := engine.Append(ctx, inputs[1])
	if err != nil {
		t.Fatal(err)
	}
	if retried.Sequence != 2 {
		t.Fatalf("retry sequence=%d", retried.Sequence)
	}
	all, err := engine.Since(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("durable rows=%d want=2", len(all))
	}
}
