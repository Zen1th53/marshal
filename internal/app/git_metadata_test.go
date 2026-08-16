package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitMetadataBindsLinkedWorktreeCommonDirectory(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, "worktree")
	gitDir := filepath.Join(root, "git", "worktrees", "task")
	commonDir := filepath.Join(root, "git")
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(gitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../..\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binds, err := gitMetadataBinds(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if len(binds) != 1 || binds[0].Source != commonDir || binds[0].Target != commonDir {
		t.Fatalf("binds=%#v, want common git directory %q", binds, commonDir)
	}
}
