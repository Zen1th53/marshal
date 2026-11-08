package projectknowledge

import (
	"errors"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"testing"
)

func TestKnowledgeRebuildAfterMoveRequiresPreviousRoot(t *testing.T) {
	root, idx := fixture(t)
	before, err := idx.Refresh(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	index, err := Open(moved, filepath.Join(moved, statepath.DirName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Query(t.Context(), Query{Mode: "overview", Limit: 10}); !errors.Is(err, ErrBinding) {
		t.Fatalf("moved cache read without rebuilding: %v", err)
	}
	for _, previous := range []string{"", moved, t.TempDir()} {
		if _, err := index.RebuildAfterMove(t.Context(), previous); !errors.Is(err, ErrBinding) {
			t.Fatalf("unbound rebuild accepted: %v", err)
		}
	}
	after, err := index.RebuildAfterMove(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head || after.Digest != before.Digest {
		t.Fatal("move changed source snapshot")
	}
	if len(query(t, index, "symbol", "Helper", "").Items) != 1 {
		t.Fatal("move lost source metadata")
	}
}

func TestKnowledgeWorktreeRejectsForeignRepositoryUnderOwnedDirectory(t *testing.T) {
	root, index := fixture(t)
	if _, err := index.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(root, statepath.DirName, "worktrees", "foreign")
	if err := os.MkdirAll(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, foreign, "init", "-b", "main")
	if _, err := OpenWorktree(foreign, root, filepath.Join(root, statepath.DirName)); !errors.Is(err, ErrBinding) {
		t.Fatalf("foreign nested repository accepted: %v", err)
	}
}
