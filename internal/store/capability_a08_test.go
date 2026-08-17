package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestCapabilityGrantExactDuplicateRequestIsIdempotent(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{ID: "PROJECT-retry", Repository: "/repo", DefaultBranch: "main", PackVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportTasks(ctx, []model.Task{{ID: "TASK-retry", Title: "retry", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	grant := capability.Grant{ID: "cap-retry", Subject: "agent-1", TaskID: "TASK-retry", Kind: capability.KindFilesystemRead, Scope: capability.Scope{Resource: "/workspace", Actions: []string{"read"}}, IssuedAt: time.Unix(100, 0).UTC(), ExpiresAt: time.Unix(200, 0).UTC(), Issuer: "admin", State: capability.GrantActive}
	if err := st.SaveGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveGrant(ctx, grant); err != nil {
		t.Fatalf("exact retry = %v, want nil", err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM capability_grants WHERE id = ?", grant.ID); got != 1 {
		t.Fatalf("grant count = %d, want 1", got)
	}
}

func TestCapabilityGrantTwoStoresRevokeHasOneDurableWinner(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "contention.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := first.InitProject(ctx, model.Project{ID: "PROJECT-race", Repository: "/repo", DefaultBranch: "main", PackVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := first.ImportTasks(ctx, []model.Task{{ID: "TASK-race", Title: "race", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	grant := capability.Grant{ID: "cap-race", Subject: "agent-1", TaskID: "TASK-race", Kind: capability.KindFilesystemRead, Scope: capability.Scope{Resource: "/workspace", Actions: []string{"read"}}, IssuedAt: time.Unix(100, 0).UTC(), ExpiresAt: time.Unix(200, 0).UTC(), Issuer: "admin", State: capability.GrantActive}
	if err := first.SaveGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	results := make(chan error, 2)
	go func() { results <- first.RevokeGrant(ctx, grant.ID, time.Unix(150, 0).UTC()) }()
	go func() { results <- second.RevokeGrant(ctx, grant.ID, time.Unix(151, 0).UTC()) }()
	var winners int
	for range 2 {
		if err := <-results; err == nil {
			winners++
		} else if !errors.Is(err, capability.ErrGrantNotFound) {
			t.Fatalf("unexpected revoke error: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want 1", winners)
	}
	got, err := first.LoadGrant(ctx, grant.ID)
	if err != nil || got.State != capability.GrantRevoked {
		t.Fatalf("stored grant = %#v err=%v", got, err)
	}
}
