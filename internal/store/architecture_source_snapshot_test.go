//go:build linux

package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func syntheticGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "gc.auto=0", "-c", "maintenance.auto=false", "-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestCommunityArchitectureReceiptBindsCanonicalSourceAndStore(t *testing.T) {
	t.Run("opened database path", testArchitectureOpenedConnectionRejectsPathReplacement)
	t.Run("source bytes modes and HEAD", testArchitectureSourceSnapshotBindsHeadModesIgnoredAndUntracked)
}

func testArchitectureSourceSnapshotBindsHeadModesIgnoredAndUntracked(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	syntheticGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "tracked"), []byte("same bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored\n"), 0644); err != nil {
		t.Fatal(err)
	}
	syntheticGit(t, root, "add", ".")
	syntheticGit(t, root, "commit", "-m", "synthetic baseline")
	base, err := SnapshotArchitectureSource(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if base.Dirty {
		t.Fatal("clean synthetic baseline reported dirty")
	}
	if err := os.Chmod(filepath.Join(root, "tracked"), 0755); err != nil {
		t.Fatal(err)
	}
	mode, err := SnapshotArchitectureSource(ctx, root)
	if err != nil || mode.Tree == base.Tree || !mode.Dirty {
		t.Fatalf("mode change not bound: %+v %v", mode, err)
	}
	if err := os.WriteFile(filepath.Join(root, "ignored"), []byte("private ignored bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	ignored, err := SnapshotArchitectureSource(ctx, root)
	if err != nil || ignored.Tree == mode.Tree {
		t.Fatalf("ignored bytes not bound: %+v %v", ignored, err)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked"), []byte("untracked bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	untracked, err := SnapshotArchitectureSource(ctx, root)
	if err != nil || untracked.Tree == ignored.Tree {
		t.Fatalf("untracked bytes not bound: %+v %v", untracked, err)
	}
	syntheticGit(t, root, "add", "tracked")
	syntheticGit(t, root, "commit", "-m", "synthetic head change")
	head, err := SnapshotArchitectureSource(ctx, root)
	if err != nil || head.Head == untracked.Head {
		t.Fatalf("HEAD not bound: %+v %v", head, err)
	}
}
