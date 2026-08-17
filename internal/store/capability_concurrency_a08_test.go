package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
)

func TestCapabilityGrantConcurrentIdempotentWritesConverge(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	grant := capability.Grant{ID: "CAP-A08-1", Subject: "agent", TaskID: "task", Kind: capability.KindShellExec, Scope: capability.Scope{Resource: "/bin/sh", Actions: []string{"exec"}}, IssuedAt: now, ExpiresAt: now.Add(time.Hour), Issuer: "admin", IdempotencyKey: "a08-retry"}
	const workers = 8
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- st.PutCapabilityGrant(ctx, grant)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent idempotent write: %v", err)
		}
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM capability_grants WHERE id = ?", grant.ID); got != 1 {
		t.Fatalf("durable grant rows=%d, want 1", got)
	}
}
