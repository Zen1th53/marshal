package secrets

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEngineLeasesUsesSecretOnlyInsideCallbackAndZeroesBytes(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	store := &memoryLeaseStore{}
	provider := providerFunc(func(context.Context, Ref) ([]byte, error) {
		return []byte("MARSHAL_TEST_SECRET_T21_A03"), nil
	})
	engine, err := NewEngine(EngineConfig{
		Store: store, Providers: map[string]Provider{"env": provider}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engine.Lease(context.Background(), LeaseRequest{
		ID: "lease-a03", Subject: "agent", TaskID: "task", Ref: Ref{Provider: "env", Name: "TOKEN", Version: "v1"},
		Purpose: "test", IssuedAt: now, ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Lease: %v", err)
	}
	if lease.State != StateLeased {
		t.Fatalf("lease state=%q, want leased", lease.State)
	}
	var observed []byte
	if err := engine.WithSecret(context.Background(), lease, func(value []byte) error {
		observed = append([]byte(nil), value...)
		return nil
	}); err != nil {
		t.Fatalf("WithSecret: %v", err)
	}
	if string(observed) != "MARSHAL_TEST_SECRET_T21_A03" {
		t.Fatalf("callback value=%q", observed)
	}
	if store.leases[lease.ID].State != StateUsed {
		t.Fatalf("stored state=%q, want used", store.leases[lease.ID].State)
	}
	if err := engine.WithSecret(context.Background(), lease, func([]byte) error { return nil }); !errors.Is(err, ErrDenied) {
		t.Fatalf("reused lease error=%v, want ErrDenied", err)
	}
}

func TestEngineFailsClosedForExpiryRevokeProviderAndCallbackFailures(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	store := &memoryLeaseStore{}
	providerCalls := 0
	engine, err := NewEngine(EngineConfig{
		Store: store,
		Providers: map[string]Provider{"env": providerFunc(func(context.Context, Ref) ([]byte, error) {
			providerCalls++
			return []byte("secret"), nil
		})},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engine.Lease(context.Background(), LeaseRequest{ID: "expired", Subject: "agent", TaskID: "task", Ref: Ref{Provider: "env", Name: "TOKEN", Version: "v1"}, Purpose: "test", IssuedAt: now, ExpiresAt: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if err := engine.WithSecret(context.Background(), lease, func([]byte) error { t.Fatal("expired callback invoked"); return nil }); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expired error=%v, want ErrLeaseExpired", err)
	}
	if providerCalls != 0 {
		t.Fatalf("provider calls=%d after expiry, want 0", providerCalls)
	}

	now = time.Unix(100, 0).UTC()
	revoked, err := engine.Lease(context.Background(), LeaseRequest{ID: "revoked", Subject: "agent", TaskID: "task", Ref: Ref{Provider: "missing", Name: "TOKEN", Version: "v1"}, Purpose: "test", IssuedAt: now, ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Revoke(context.Background(), RevokeRequest{LeaseID: revoked.ID, Subject: "agent"}); err != nil {
		t.Fatal(err)
	}
	if err := engine.WithSecret(context.Background(), revoked, func([]byte) error { t.Fatal("revoked callback invoked"); return nil }); !errors.Is(err, ErrDenied) {
		t.Fatalf("revoked error=%v, want ErrDenied", err)
	}

	failed, err := engine.Lease(context.Background(), LeaseRequest{ID: "callback-failed", Subject: "agent", TaskID: "task", Ref: Ref{Provider: "env", Name: "TOKEN", Version: "v1"}, Purpose: "test", IssuedAt: now, ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.WithSecret(context.Background(), failed, func([]byte) error { return errors.New("callback failure") }); !errors.Is(err, ErrDenied) {
		t.Fatalf("callback error=%v, want ErrDenied", err)
	}
	if store.leases[failed.ID].State != StateLeased {
		t.Fatalf("callback failure state=%q, want leased", store.leases[failed.ID].State)
	}
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
