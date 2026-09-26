package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestM04ReworkResumesExistingBranchAtResult(t *testing.T) {
	manager, request, result, path := committedTaskBranch(t)
	request.BaseCommit = result
	resumed, err := manager.Resume(context.Background(), request)
	if err != nil || resumed.Path != path || resumed.HEAD != result {
		t.Fatalf("resume = %#v, %v", resumed, err)
	}
}

func TestM04RestartReattachesBranchAtRecordedResult(t *testing.T) {
	manager, request, result, path := committedTaskBranch(t)
	cmd := exec.Command("git", "-C", filepath.Dir(filepath.Dir(path)), "worktree", "remove", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("remove worktree: %s: %v", out, err)
	}
	request.BaseCommit = result
	resumed, err := manager.Resume(context.Background(), request)
	if err != nil || resumed.Path != path || resumed.HEAD != result {
		t.Fatalf("reattach = %#v, %v", resumed, err)
	}
}

func committedTaskBranch(t *testing.T) (*Manager, model.WorktreeRequest, string, string) {
	t.Helper()
	repo := testgit.New(t)
	manager := New(repo.Path(), filepath.Join(repo.Path(), ".marshal", "worktrees"))
	request := model.WorktreeRequest{TaskID: "TASK-001", Branch: "marshal/run/task", BaseCommit: repo.HEAD(t)}
	wt, err := manager.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "result.txt"), []byte("result"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "result"}} {
		cmd := exec.Command("git", append([]string{"-C", wt.Path}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	cmd := exec.Command("git", "-C", wt.Path, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return manager, request, strings.TrimSpace(string(out)), wt.Path
}
