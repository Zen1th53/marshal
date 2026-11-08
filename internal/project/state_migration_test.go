//go:build !governed

package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestStateMigrationRepairsWorktreesAndPreservesProjectFiles(t *testing.T) {
	repo := testgit.New(t)
	root := repo.Path()
	old := filepath.Join(root, ".marshal-extreme")
	tree := filepath.Join(old, "worktrees", "task")
	cmd := exec.Command("git", "-C", root, "worktree", "add", "--detach", tree, "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	body := []byte("project source mentioning " + old)
	if err := os.WriteFile(filepath.Join(tree, "source.txt"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(root); err != nil {
		t.Fatal(err)
	}
	moved := statepath.Path(root, "worktrees", "task")
	got, err := os.ReadFile(filepath.Join(moved, "source.txt"))
	if err != nil || string(got) != string(body) {
		t.Fatal("rewrote project files", string(got), err)
	}
	cmd = exec.Command("git", "-C", root, "worktree", "list", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "worktree "+moved+"\n") || strings.Contains(string(out), "worktree "+tree+"\n") {
		t.Fatal(string(out), err)
	}
	cmd = exec.Command("git", "-C", moved, "rev-parse", "--show-toplevel")
	out, err = cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != moved {
		t.Fatal(string(out), err)
	}
}
