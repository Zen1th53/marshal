package secrets

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/events"
)

func TestEngineDeniesExpiredLease(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	store := &memoryLeaseStore{}
	engine, err := NewEngine(EngineConfig{
		Store: store, Capability: allowSecretCapability{},
		Providers: map[string]Provider{"env": providerFunc(func(context.Context, Ref) ([]byte, error) { return []byte("x"), nil })},
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engine.Lease(context.Background(), LeaseRequest{
		ID: "expired", Subject: "agent", TaskID: "task", Ref: Ref{Provider: "env", Name: "TOKEN", Version: "v1"},
		Purpose: "test", IssuedAt: now.Add(-2 * time.Minute), ExpiresAt: now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = engine.WithSecret(context.Background(), lease, func([]byte) error { return nil })
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expected ErrLeaseExpired, got %v", err)
	}
}

func TestEngineDeniesWhenCapabilityCheckFails(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	store := &memoryLeaseStore{}
	providerCalls := 0
	engine, err := NewEngine(EngineConfig{
		Store: store, Capability: denySecretCapability{},
		Providers: map[string]Provider{"env": providerFunc(func(context.Context, Ref) ([]byte, error) { providerCalls++; return []byte("x"), nil })},
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engine.Lease(context.Background(), LeaseRequest{ID: "authz", Subject: "agent", TaskID: "task", Ref: Ref{Provider: "env", Name: "TOKEN", Version: "v1"}, Purpose: "test", IssuedAt: time.Unix(100, 0).UTC(), ExpiresAt: time.Unix(200, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.WithSecret(context.Background(), lease, func([]byte) error { t.Fatal("denied callback invoked"); return nil }); !errors.Is(err, ErrDenied) {
		t.Fatalf("authorization error=%v, want ErrDenied", err)
	}
	if providerCalls != 0 {
		t.Fatalf("provider calls=%d, want 0", providerCalls)
	}
}

func TestEngineAuthorizesNormalizedSecretReference(t *testing.T) {
	capabilityCheck := &captureSecretCapability{}
	now := time.Unix(100, 0).UTC()
	store := &memoryLeaseStore{}
	engine, err := NewEngine(EngineConfig{
		Store: store, Capability: capabilityCheck,
		Providers: map[string]Provider{"env": providerFunc(func(context.Context, Ref) ([]byte, error) { return []byte("x"), nil })},
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engine.Lease(context.Background(), LeaseRequest{ID: "normalized", Subject: "agent", TaskID: "task", Ref: Ref{Provider: "env", Name: "TOKEN", Version: "v1"}, Purpose: "test", IssuedAt: now, ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.WithSecret(context.Background(), lease, func([]byte) error { return nil }); err != nil {
		t.Fatalf("WithSecret: %v", err)
	}
	if capabilityCheck.query.Resource != "secret://env/TOKEN/v1" || capabilityCheck.query.Action != "read" || capabilityCheck.query.Kind != capability.KindSecretUse {
		t.Fatalf("authorization query=%#v", capabilityCheck.query)
	}
}

func TestEngineEmitsReferenceOnlySecretLifecycleEvents(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	eventStore := &memoryEventStore{}
	engine, err := NewEngine(EngineConfig{
		Store: &memoryLeaseStore{}, Capability: allowSecretCapability{}, EventStore: eventStore,
		Providers: map[string]Provider{"env": providerFunc(func(context.Context, Ref) ([]byte, error) { return []byte("secret"), nil })},
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Lease: %v events=%#v", err, eventStore.events)
	}
	lease, err := engine.Lease(context.Background(), LeaseRequest{ID: "events", Subject: "agent", TaskID: "task", Ref: Ref{Provider: "env", Name: "TOKEN", Version: "v1"}, Purpose: "test", IssuedAt: now, ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("Lease: %v events=%#v", err, eventStore.events)
	}
	if err := engine.WithSecret(context.Background(), lease, func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := engine.WithSecret(context.Background(), lease, func([]byte) error { t.Fatal("terminal retry callback invoked"); return nil }); err != nil {
		t.Fatalf("terminal retry: %v", err)
	}
	want := []events.EventType{events.EventTypeSecretLeaseRequested, events.EventTypeSecretLeaseIssued, events.EventTypeSecretAccessUsed}
	if len(eventStore.events) != len(want) {
		t.Fatalf("event count=%d, want %d", len(eventStore.events), len(want))
	}
	for i, event := range eventStore.events {
		if event.Type != want[i] || event.Subject != "agent" || event.TaskID != "task" || event.Data["secret_value"] != nil {
			t.Fatalf("event[%d]=%#v", i, event)
		}
	}
}

func TestEngineReconcilesDurableUseWhenEventDeliveryFails(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	eventStore := &flakyEventStore{failUsed: true}
	store := &memoryLeaseStore{}
	providerCalls := 0
	engine, err := NewEngine(EngineConfig{
		Store: store, Capability: allowSecretCapability{}, EventStore: eventStore,
		Providers: map[string]Provider{"env": providerFunc(func(context.Context, Ref) ([]byte, error) { providerCalls++; return []byte("secret"), nil })},
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engine.Lease(context.Background(), LeaseRequest{ID: "event-failure", Subject: "agent", TaskID: "task", Ref: Ref{Provider: "env", Name: "TOKEN", Version: "v1"}, Purpose: "test", IssuedAt: now, ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.WithSecret(context.Background(), lease, func([]byte) error { return nil }); !errors.Is(err, ErrDenied) {
		t.Fatalf("first use error=%v, want ErrDenied", err)
	}
	if store.leases[lease.ID].State != StateUsed || providerCalls != 1 {
		t.Fatalf("durable state=%q provider calls=%d", store.leases[lease.ID].State, providerCalls)
	}
	eventStore.failUsed = false
	if err := engine.WithSecret(context.Background(), lease, func([]byte) error { t.Fatal("reconciliation callback invoked"); return nil }); err != nil {
		t.Fatalf("reconciliation error=%v", err)
	}
	if providerCalls != 1 {
		t.Fatalf("provider calls after reconciliation=%d, want 1", providerCalls)
	}
}

func TestEngineCancellationReleasesExecutionClaim(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	store := &memoryLeaseStore{}
	engine, err := NewEngine(EngineConfig{
		Store: store, Capability: allowSecretCapability{},
		Providers: map[string]Provider{"env": providerFunc(func(ctx context.Context, _ Ref) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() })},
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engine.Lease(context.Background(), LeaseRequest{ID: "cancel", Subject: "agent", TaskID: "task", Ref: Ref{Provider: "env", Name: "TOKEN", Version: "v1"}, Purpose: "test", IssuedAt: now, ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := engine.WithSecret(ctx, lease, func([]byte) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v, want context.Canceled", err)
	}
	if store.leases[lease.ID].AccessOwner != "" || store.leases[lease.ID].State != StateLeased {
		t.Fatalf("claim after cancellation=%#v", store.leases[lease.ID])
	}
}

type memoryEventStore struct{ events []events.Event }

type flakyEventStore struct {
	memoryEventStore
	failUsed bool
}

func (s *flakyEventStore) Append(ctx context.Context, event events.Event) (events.Event, error) {
	if s.failUsed && event.Type == events.EventTypeSecretAccessUsed {
		return events.Event{}, errors.New("downstream event unavailable")
	}
	return s.memoryEventStore.Append(ctx, event)
}

func (s *memoryEventStore) Append(_ context.Context, event events.Event) (events.Event, error) {
	if err := event.Validate(); err != nil {
		return events.Event{}, err
	}
	for _, existing := range s.events {
		if existing.IdempotencyKey == event.IdempotencyKey {
			return existing, nil
		}
	}
	s.events = append(s.events, event)
	return event, nil
}
func (s *memoryEventStore) Since(context.Context, events.Sequence) ([]events.Event, error) {
	return nil, nil
}

type denySecretCapability struct{}

type allowSecretCapability struct{}

type captureSecretCapability struct{ query capability.Query }

func (c *captureSecretCapability) Grant(context.Context, capability.GrantRequest) (capability.Grant, error) {
	return capability.Grant{}, nil
}
func (c *captureSecretCapability) Authorize(_ context.Context, query capability.Query) (capability.Decision, error) {
	c.query = query
	return capability.Decision{Outcome: capability.OutcomeAllow}, nil
}
func (c *captureSecretCapability) Revoke(context.Context, capability.RevokeRequest) error { return nil }

func (allowSecretCapability) Grant(context.Context, capability.GrantRequest) (capability.Grant, error) {
	return capability.Grant{}, nil
}
func (allowSecretCapability) Authorize(context.Context, capability.Query) (capability.Decision, error) {
	return capability.Decision{Outcome: capability.OutcomeAllow, Reason: capability.CodeDenied}, nil
}
func (allowSecretCapability) Revoke(context.Context, capability.RevokeRequest) error { return nil }

func (denySecretCapability) Grant(context.Context, capability.GrantRequest) (capability.Grant, error) {
	return capability.Grant{}, capability.ErrDenied
}
func (denySecretCapability) Authorize(context.Context, capability.Query) (capability.Decision, error) {
	return capability.Decision{Outcome: capability.OutcomeDeny, Reason: capability.CodeDenied}, nil
}
func (denySecretCapability) Revoke(context.Context, capability.RevokeRequest) error {
	return capability.ErrDenied
}

type memoryLeaseStore struct{ leases map[string]Lease }

func (s *memoryLeaseStore) PutSecretLease(_ context.Context, lease Lease) error {
	if s.leases == nil {
		s.leases = make(map[string]Lease)
	}
	if _, ok := s.leases[lease.ID]; ok {
		return ErrDenied
	}
	s.leases[lease.ID] = lease
	return nil
}
func (s *memoryLeaseStore) GetSecretLease(_ context.Context, id string) (Lease, error) {
	lease, ok := s.leases[id]
	if !ok {
		return Lease{}, ErrNotFound
	}
	return lease, nil
}
func (s *memoryLeaseStore) TransitionSecretLease(_ context.Context, id string, from, to LeaseState, _ time.Time) (Lease, error) {
	lease, ok := s.leases[id]
	if !ok || lease.State != from {
		return Lease{}, ErrDenied
	}
	lease.State = to
	s.leases[id] = lease
	return lease, nil
}

func (s *memoryLeaseStore) ClaimSecretLease(_ context.Context, id, owner string, at time.Time) (Lease, error) {
	lease, ok := s.leases[id]
	if !ok || lease.State != StateLeased || lease.AccessOwner != "" {
		return Lease{}, ErrDenied
	}
	lease.AccessOwner = owner
	lease.AccessClaimedAt = &at
	s.leases[id] = lease
	return lease, nil
}

func (s *memoryLeaseStore) CompleteSecretLease(_ context.Context, id, owner string, _ time.Time) (Lease, error) {
	lease, ok := s.leases[id]
	if !ok || lease.State != StateLeased || lease.AccessOwner != owner {
		return Lease{}, ErrDenied
	}
	lease.State = StateUsed
	lease.AccessOwner = ""
	lease.AccessClaimedAt = nil
	s.leases[id] = lease
	return lease, nil
}

func (s *memoryLeaseStore) ReleaseSecretLeaseClaim(_ context.Context, id, owner string) error {
	lease, ok := s.leases[id]
	if !ok || lease.AccessOwner != owner {
		return ErrDenied
	}
	lease.AccessOwner = ""
	lease.AccessClaimedAt = nil
	s.leases[id] = lease
	return nil
}
