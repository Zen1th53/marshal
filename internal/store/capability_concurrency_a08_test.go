package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
)

func TestCapabilityGrantTwoStoresOneDurableExecution(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/state.db"
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	stores := []*Store{first, second}
	now := time.Date(2026, 8, 16, 18, 0, 0, 0, time.UTC)
	request := capability.GrantRequest{
		Subject: "agent-a08", TaskID: "task-a08", Kind: capability.KindGitCommit,
		Scope:     capability.Scope{Resource: "repo-a08", Actions: []string{"commit"}},
		ExpiresAt: now.Add(time.Hour), Issuer: "broker", IdempotencyKey: "a08-same-request",
	}
	start := make(chan struct{})
	results := make(chan capability.Grant, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i, st := range stores {
		wg.Add(1)
		go func(i int, st *Store) {
			defer wg.Done()
			<-start
			engine := capability.NewAuthorizedEngine(st, func() time.Time { return now }, capabilityTestAuthority{})
			grant, err := engine.Grant(ctx, request)
			results <- grant
			errs <- err
			_ = i
		}(i, st)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	var grants []capability.Grant
	for grant := range results {
		grants = append(grants, grant)
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("contention error=%v", err)
		}
	}
	if len(grants) != 2 || grants[0].ID != grants[1].ID {
		t.Fatalf("contention grants=%#v", grants)
	}
	if got := queryInt(t, first.db, "SELECT count(*) FROM capability_grants"); got != 1 {
		t.Fatalf("durable grant rows=%d want 1", got)
	}
}

func TestCapabilityGrantHighContentionConverges(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/state.db"
	bootstrap, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	bootstrap.Close()
	const contenders = 64
	stores := make([]*Store, 8)
	for i := range stores {
		stores[i], err = Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		defer stores[i].Close()
	}
	now := time.Date(2026, 8, 16, 18, 0, 0, 0, time.UTC)
	request := capability.GrantRequest{
		Subject: "agent-a08-high", TaskID: "task-a08-high", Kind: capability.KindGitCommit,
		Scope:     capability.Scope{Resource: "repo-a08-high", Actions: []string{"commit"}},
		ExpiresAt: now.Add(time.Hour), Issuer: "broker", IdempotencyKey: "a08-high-request",
	}
	start := make(chan struct{})
	errs := make(chan error, contenders)
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			engine := capability.NewAuthorizedEngine(stores[i%len(stores)], func() time.Time { return now }, capabilityTestAuthority{})
			_, err := engine.Grant(ctx, request)
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("high contention error=%v", err)
		}
	}
	if got := queryInt(t, stores[0].db, "SELECT count(*) FROM capability_grants WHERE idempotency_key = ?", request.IdempotencyKey); got != 1 {
		t.Fatalf("high contention rows=%d want 1", got)
	}
}
