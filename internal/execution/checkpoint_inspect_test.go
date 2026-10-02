package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyAndDiffSnapshots(t *testing.T) {
	root := t.TempDir()
	ce, err := NewCheckpointEngine(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	writeFile(t, filepath.Join(root, "keep.txt"), "same")
	writeFile(t, filepath.Join(root, "edit.txt"), "before")
	writeFile(t, filepath.Join(root, "gone.txt"), "x")
	first, err := ce.CaptureCheckpoint(ctx, "run", "task", "first")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "edit.txt"), "after")
	if err := os.Remove(filepath.Join(root, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "dir", "new.txt"), "new")
	second, err := ce.CaptureCheckpoint(ctx, "run", "task", "second")
	if err != nil {
		t.Fatal(err)
	}

	check, err := ce.VerifySnapshot(first.CheckpointID)
	if err != nil || check.State != SnapshotIntact {
		t.Fatalf("intact: %+v %v", check, err)
	}
	diff, err := ce.DiffSnapshots(first.CheckpointID, second.CheckpointID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(diff.Added, []string{"dir/new.txt"}) || !reflect.DeepEqual(diff.Removed, []string{"gone.txt"}) ||
		!reflect.DeepEqual(diff.Changed, []string{"edit.txt"}) || diff.Truncated {
		t.Fatalf("diff: %+v", diff)
	}
	if limited, err := ce.DiffSnapshots(first.CheckpointID, second.CheckpointID, 1); err != nil || !limited.Truncated ||
		len(limited.Added)+len(limited.Removed)+len(limited.Changed) != 1 {
		t.Fatalf("limit: %+v %v", limited, err)
	}

	// Changing a byte inside the snapshot is detected and refuses comparison.
	writeFile(t, filepath.Join(second.WorktreePath, "keep.txt"), "forged")
	if check, _ := ce.VerifySnapshot(second.CheckpointID); check.State != SnapshotTampered {
		t.Fatalf("tampered: %+v", check)
	}
	if _, err := ce.DiffSnapshots(first.CheckpointID, second.CheckpointID, 100); !errors.Is(err, ErrCheckpointFailed) {
		t.Fatalf("diff with a tampered snapshot: %v", err)
	}

	if err := os.RemoveAll(first.WorktreePath); err != nil {
		t.Fatal(err)
	}
	if check, _ := ce.VerifySnapshot(first.CheckpointID); check.State != SnapshotMissing {
		t.Fatalf("missing: %+v", check)
	}
	if _, err := ce.VerifySnapshot("../escape"); !errors.Is(err, ErrCheckpointFailed) {
		t.Fatalf("path traversal id: %v", err)
	}
}
