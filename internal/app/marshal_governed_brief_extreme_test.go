//go:build !governed

package app

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
)

// captureProcess05ProviderContext records the trusted context of the actual
// provider request the owned fixture adapter receives.
func captureProcess05ProviderContext(t *testing.T, r *Runtime, name string) func() (string, bool) {
	t.Helper()
	client, ok := r.adapters[name].(*requiredOwnedFixtureAdapter)
	if !ok || client.handle == nil {
		t.Fatal("owned adapter fixture is not registered")
	}
	var seen atomic.Value
	next := client.handle
	client.handle = func(ctx context.Context, req adapter.Request) (adapter.Result, error) {
		seen.Store(req.TrustedContext)
		return next(ctx, req)
	}
	return func() (string, bool) {
		text, ok := seen.Load().(string)
		return text, ok
	}
}
