package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return "<missing>"
	}
	return string(data)
}

func TestCheckpointCaptureAndVerifiedRestore(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	root := repo.Path()
	write := func(name, body string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("keep.txt", "v1")
	write("gone.txt", "will be deleted")
	r, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := local.Context(ctx)
	env := func(key, target string) CommandEnvelope {
		return CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "S", TargetID: target, IdempotencyKey: key}
	}

	if _, err := r.CommandCaptureCheckpoint(ctx, env("c0", "checkpoint:new"), "before"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous capture: %v", err)
	}
	cp, err := r.CommandCaptureCheckpoint(owner, env("c1", "checkpoint:new"), "before edits")
	if err != nil || cp.SnapshotDigest == "" {
		t.Fatalf("capture: %+v %v", cp, err)
	}

	write("keep.txt", "v2")
	write("added/new.txt", "new")
	if err := os.Remove(filepath.Join(root, "gone.txt")); err != nil {
		t.Fatal(err)
	}

	check, diff, err := r.Execution().Engine().PreviewRestore(ctx, cp.CheckpointID)
	if err != nil || check.State != execution.SnapshotIntact ||
		!slices.Contains(diff.Added, "gone.txt") || !slices.Contains(diff.Removed, "added/new.txt") || !slices.Contains(diff.Changed, "keep.txt") {
		t.Fatalf("preview: %+v %+v %v", check, diff, err)
	}

	if _, err := r.CommandRestoreCheckpoint(owner, env("r0", "checkpoint:"+cp.CheckpointID), "sha-not-confirmed"); !errors.Is(err, execution.ErrCheckpointFailed) {
		t.Fatalf("restore with an unconfirmed digest: %v", err)
	}
	if readFile(t, filepath.Join(root, "keep.txt")) != "v2" {
		t.Fatal("a refused restore changed files")
	}

	result, err := r.CommandRestoreCheckpoint(owner, env("r1", "checkpoint:"+cp.CheckpointID), cp.SnapshotDigest)
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(root, "keep.txt")) != "v1" || readFile(t, filepath.Join(root, "gone.txt")) != "will be deleted" ||
		readFile(t, filepath.Join(root, "added/new.txt")) != "<missing>" {
		t.Fatal("project not restored to the checkpoint")
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Fatal("restore touched .git")
	}
	if result.Recovery.CheckpointID == "" {
		t.Fatal("no recovery point")
	}
	if _, err := r.CommandRestoreCheckpoint(owner, env("r1", "checkpoint:"+cp.CheckpointID), cp.SnapshotDigest); err == nil {
		t.Fatal("a replayed restore ran again")
	}

	// The recovery point undoes the restore.
	if _, err := r.CommandRestoreCheckpoint(owner, env("r2", "checkpoint:"+result.Recovery.CheckpointID), result.Recovery.SnapshotDigest); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(root, "keep.txt")) != "v2" || readFile(t, filepath.Join(root, "added/new.txt")) != "new" {
		t.Fatal("recovery point did not bring the edits back")
	}
}

func TestRestoreRefusedWhileARunIsActive(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	binding, _ := projectid.LoadBinding(filepath.Join(repo.Path(), projectid.StateDirName))
	store, err := execution.NewFileRunStore(filepath.Join(repo.Path(), ".marshal", "execution", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.CreateRun(ctx, execution.ExecutionRun{RunID: "RUN-busy", ProjectID: binding.ID, SessionID: "OTHER", State: execution.RunRunning, StartedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	local, _ := r.OpenLocalControl(ctx)
	owner := local.Context(ctx)
	cp, err := r.CommandCaptureCheckpoint(owner, CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "S", TargetID: "checkpoint:new", IdempotencyKey: "c"}, "x")
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.CommandRestoreCheckpoint(owner, CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "S", TargetID: "checkpoint:" + cp.CheckpointID, IdempotencyKey: "r"}, cp.SnapshotDigest)
	if !errors.Is(err, execution.ErrCheckpointFailed) {
		t.Fatalf("restore with a running run of another session: %v", err)
	}
}
