package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestReconcileContainsFileStateAfterSymlinks(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	outside := t.TempDir()
	for _, dir := range []string{repo.Path(), outside} {
		if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"project":{"branch":"outside"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{
		"inside-link.json":  filepath.Join(repo.Path(), "state.json"),
		"outside-link.json": filepath.Join(outside, "state.json"),
		"outside-dir":       outside,
		"inside-dir":        repo.Path(),
	} {
		if err := os.Symlink(target, filepath.Join(repo.Path(), name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name    string
		path    string
		invalid bool
	}{
		{"file", filepath.Join(repo.Path(), "state.json"), false},
		{"inside symlink", filepath.Join(repo.Path(), "inside-link.json"), false},
		{"inside directory symlink", filepath.Join(repo.Path(), "inside-dir", "state.json"), false},
		{"outside symlink", filepath.Join(repo.Path(), "outside-link.json"), true},
		{"outside directory symlink", filepath.Join(repo.Path(), "outside-dir", "state.json"), true},
		{"absolute outside", filepath.Join(outside, "state.json"), true},
		{"traversal", filepath.Join(repo.Path(), "..", filepath.Base(outside), "state.json"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runtime.Reconcile(ctx, ReconcileRequest{FileState: tc.path})
			if tc.invalid {
				if !errors.Is(err, model.ErrInvalid) {
					t.Fatalf("Reconcile(%q) = %v, want invalid path", tc.path, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
