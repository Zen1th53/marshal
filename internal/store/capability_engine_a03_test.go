package store

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
)

func TestCapabilityEngineUsesCanonicalStoreAndSurvivesReload(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/state.db"
	now := time.Date(2026, 8, 16, 15, 0, 0, 0, time.UTC)
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	engine := capability.NewEngine(first, func() time.Time { return now })
	grant, err := engine.Grant(ctx, capability.GrantRequest{
		Subject: "agent-store", TaskID: "task-store", Kind: capability.KindFilesystemRead,
		Scope:     capability.Scope{Resource: "/workspace/store", Actions: []string{"read"}},
		ExpiresAt: now.Add(time.Hour), Issuer: "broker", IdempotencyKey: "store-request-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	restarted := capability.NewEngine(second, func() time.Time { return now })
	retry, err := restarted.Grant(ctx, capability.GrantRequest{
		Subject: "agent-store", TaskID: "task-store", Kind: capability.KindFilesystemRead,
		Scope:     capability.Scope{Resource: "/workspace/store", Actions: []string{"read"}},
		ExpiresAt: now.Add(time.Hour), Issuer: "broker", IdempotencyKey: "store-request-1",
	})
	if err != nil || retry.ID != grant.ID {
		t.Fatalf("retry=%#v err=%v original=%#v", retry, err, grant)
	}
	decision, err := restarted.Authorize(ctx, capability.Query{
		Subject: "agent-store", TaskID: "task-store", Kind: capability.KindFilesystemRead,
		Resource: "/workspace/store", Action: "read", At: now,
	})
	if err != nil || decision.Outcome != capability.OutcomeAllow || decision.MatchedGrant != grant.ID {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
}
