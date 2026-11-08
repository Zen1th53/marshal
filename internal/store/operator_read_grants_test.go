package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOperatorReadGrantPersistenceAndRevoke(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	first, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := first.SetOperatorReadGrant(ctx, "project-one", "/provider/history", true); err != nil {
		t.Fatal(err)
	}
	if err := first.SetOperatorReadGrant(ctx, "project-one", "/provider/history/private", false); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	grants, err := second.ListOperatorReadGrants(ctx, "project-one")
	if err != nil || len(grants) != 2 || !grants[0].Allowed || grants[1].Allowed {
		t.Fatalf("persisted grants=%+v err=%v", grants, err)
	}
	if err := second.RevokeOperatorReadGrant(ctx, "project-one", "/provider/history/private"); err != nil {
		t.Fatal(err)
	}
	grants, err = second.ListOperatorReadGrants(ctx, "project-one")
	if err != nil || len(grants) != 1 || grants[0].Folder != "/provider/history" {
		t.Fatalf("grants after revoke=%+v err=%v", grants, err)
	}
}
