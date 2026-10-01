//go:build linux

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestCoordinatedStateRestore(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "state.db")
	meta, err := r.Store().Backup(ctx, backup)
	if err != nil {
		t.Fatal(err)
	}
	// State that the backup does not contain.
	if _, err := r.RegisterAgent(ctx, RegisterAgentRequest{Name: "after-backup", Role: model.RoleDeveloper}); err != nil {
		t.Fatal(err)
	}
	agents := func(rt *Runtime) int {
		list, err := rt.Agents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return len(list)
	}
	before := agents(r)
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	env := func(key string) CommandEnvelope {
		return CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "S", TargetID: "state:" + backup, IdempotencyKey: key}
	}

	// Wrong digest: refused, the previous state is reopened and intact.
	result, err := r.CommandRestoreState(local.Context(ctx), env("w"), backup, "sha256:0000")
	if err == nil {
		t.Fatal("restore with a wrong digest succeeded")
	}
	r = result.Runtime
	if r == nil || agents(r) != before {
		t.Fatal("refused restore did not leave the previous state usable")
	}

	// Another open handle on the database (a second window): refused.
	layout, err := project.Discover(repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.Open(ctx, layout.Database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.SchemaVersion(ctx); err != nil {
		t.Fatal(err)
	}
	local, _ = r.OpenLocalControl(ctx)
	result, err = r.CommandRestoreState(local.Context(ctx), env("busy"), backup, meta.DatabaseSHA256)
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("restore while another handle holds the database: %v", err)
	}
	r = result.Runtime
	other.Close()

	// The real restore.
	local, _ = r.OpenLocalControl(ctx)
	result, err = r.CommandRestoreState(local.Context(ctx), env("ok"), backup, meta.DatabaseSHA256)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { result.Runtime.Close() })
	if agents(result.Runtime) != before-1 {
		t.Fatalf("state not restored: %d agents, want %d", agents(result.Runtime), before-1)
	}
	if _, err := os.Stat(result.RecoveryPath); err != nil {
		t.Fatalf("no recovery backup: %v", err)
	}
	if _, err := store.VerifyBackup(ctx, result.RecoveryPath, "", 0); err != nil {
		t.Fatalf("recovery backup does not verify: %v", err)
	}
}
