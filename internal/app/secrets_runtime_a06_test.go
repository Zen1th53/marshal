package app

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/secrets"
)

func TestRuntimeDelegatesSecretUseThroughConfiguredBroker(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	broker := &runtimeSecretBroker{}
	runtime, err := OpenWithOptions(context.Background(), repo.Path(), Options{SecretBroker: broker})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	if err := runtime.WithSecret(context.Background(), secrets.Lease{ID: "lease-a06"}, func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !broker.called {
		t.Fatal("runtime did not delegate through secret broker")
	}
}

type runtimeSecretBroker struct{ called bool }

func (b *runtimeSecretBroker) Lease(context.Context, secrets.LeaseRequest) (secrets.Lease, error) {
	return secrets.Lease{}, nil
}
func (b *runtimeSecretBroker) WithSecret(_ context.Context, _ secrets.Lease, use func([]byte) error) error {
	b.called = true
	return use([]byte("runtime-secret"))
}
func (b *runtimeSecretBroker) Revoke(context.Context, secrets.RevokeRequest) error { return nil }
