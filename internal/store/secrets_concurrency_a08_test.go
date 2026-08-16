package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/secrets"
)

func TestSecretLeaseTwoStoresExecuteProviderOnce(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "secret.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.ExecContext(ctx, `INSERT INTO projects(project_id, repository, default_branch, pack_version, created_at) VALUES('project-a08', '/repo', 'main', '1', '2026-01-01T00:00:00Z'); INSERT INTO tasks(task_id, project_id, title, status, risk, revision, created_at, updated_at) VALUES('task-a08', 'project-a08', 'secret', 'proposed', 'R1', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.Close(); second.Close() })
	var providerCalls atomic.Int32
	provider := countingSecretProvider{calls: &providerCalls}
	engineA, err := secrets.NewEngine(secrets.EngineConfig{Store: first, Capability: allowingSecretCapability{}, Providers: map[string]secrets.Provider{"env": provider}, Now: func() time.Time { return time.Unix(100, 0).UTC() }})
	if err != nil {
		t.Fatal(err)
	}
	engineB, err := secrets.NewEngine(secrets.EngineConfig{Store: second, Capability: allowingSecretCapability{}, Providers: map[string]secrets.Provider{"env": provider}, Now: func() time.Time { return time.Unix(100, 0).UTC() }})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engineA.Lease(ctx, secrets.LeaseRequest{ID: "lease-a08", Subject: "agent", TaskID: "task-a08", Ref: secrets.Ref{Provider: "env", Name: "TOKEN", Version: "v1"}, Purpose: "test", IssuedAt: time.Unix(100, 0).UTC(), ExpiresAt: time.Unix(200, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, engine := range []*secrets.Engine{engineA, engineB} {
		wg.Add(1)
		go func(engine *secrets.Engine) {
			defer wg.Done()
			<-start
			results <- engine.WithSecret(ctx, lease, func([]byte) error { return nil })
		}(engine)
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for result := range results {
		if result == nil {
			successes++
		} else if !errors.Is(result, secrets.ErrDenied) {
			t.Fatalf("unexpected contention error=%v", result)
		}
	}
	if (successes != 1 && successes != 2) || providerCalls.Load() != 1 {
		t.Fatalf("successes=%d provider calls=%d, want one execution and canonical retry reconciliation", successes, providerCalls.Load())
	}
}

type countingSecretProvider struct{ calls *atomic.Int32 }

func (p countingSecretProvider) Resolve(context.Context, secrets.Ref) ([]byte, error) {
	p.calls.Add(1)
	return []byte("secret"), nil
}

type allowingSecretCapability struct{}

func (allowingSecretCapability) Grant(context.Context, capability.GrantRequest) (capability.Grant, error) {
	return capability.Grant{}, nil
}
func (allowingSecretCapability) Authorize(context.Context, capability.Query) (capability.Decision, error) {
	return capability.Decision{Outcome: capability.OutcomeAllow}, nil
}
func (allowingSecretCapability) Revoke(context.Context, capability.RevokeRequest) error { return nil }
