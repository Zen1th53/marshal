package hostgit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

// A task tree reached through a symlinked ancestor (macOS /var and /tmp are
// links into /private) must match the pin recorded for it, whichever spelling
// records or restores it.
func TestRepositoryPinMatchesSymlinkedTreeSpelling(t *testing.T) {
	repo := testgit.New(t)
	tree := filepath.Join(testgit.Canonical(t, t.TempDir()), "TASK-1")
	if out, err := exec.Command("git", "-C", repo.Path(), "worktree", "add", "--detach", tree, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v %s", err, out)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Dir(tree), alias); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(alias, filepath.Base(tree))

	if err := RecordRepository(repo.Path(), linked); err != nil {
		t.Fatalf("record through symlink: %v", err)
	}
	for _, spelling := range []string{linked, tree} {
		if err := RestoreRepository(repo.Path(), spelling); err != nil {
			t.Fatalf("restore %s: %v", spelling, err)
		}
		worktree, _, pinned, err := pinnedPaths(spelling)
		if err != nil || !pinned || worktree != tree {
			t.Fatalf("pinned %s: %q %v %v", spelling, worktree, pinned, err)
		}
	}
}
