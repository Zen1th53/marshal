package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

// clearGitIdentity leaves Git no identity to find for repo: no repository,
// global, system or environment identity, and a home of its own.
func clearGitIdentity(t *testing.T, repo string) {
	t.Helper()
	marshalGit(t, repo, "config", "--unset", "user.name")
	marshalGit(t, repo, "config", "--unset", "user.email")
	empty := filepath.Join(t.TempDir(), "empty-gitconfig")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_SYSTEM", empty)
	for _, key := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "EMAIL"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
}

// A task that will write files is never handed to a worker in a repository
// where Git has no identity: MARSHAL could not commit the result, so the
// model run would be wasted (the macOS demo spent seven per task). The run
// pauses with the instruction instead, and resuming after the owner sets an
// identity dispatches normally.
func TestDispatchWithoutGitIdentityPausesBeforeTheWorker(t *testing.T) {
	ctx := context.Background()
	s, repo := marshalFixture(t, 1)
	var launches atomic.Int32
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		launches.Add(1)
		return nil, os.WriteFile(filepath.Join(req.Worktree, req.Task.PlanTaskID+".txt"), []byte("done\n"), 0o600)
	}}
	if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	clearGitIdentity(t, repo)
	_, err := s.Dispatch(ctx, "run", "a", "write file")
	if err == nil || !strings.Contains(err.Error(), "user.name") {
		t.Fatalf("dispatch error = %v, want the missing Git identity", err)
	}
	if n := launches.Load(); n != 0 {
		t.Fatalf("worker launched %d times without a Git identity", n)
	}
	run, _, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != marshal.AwaitingUser || run.Pause == nil || run.Pause.Code != marshal.CodeGitIdentity || run.Pause.Action != marshal.PauseResume {
		t.Fatalf("run %s, pause %+v", run.State, run.Pause)
	}
	if !strings.Contains(run.Pause.Reason, "git config --global user.name") {
		t.Fatalf("pause reason %q gives no instruction", run.Pause.Reason)
	}
	if task := run.Tasks[taskIndex(run, "a")]; task.State != marshal.Queued || task.InfraRetries != 0 {
		t.Fatalf("task %s after refusal, infra retries %d", task.State, task.InfraRetries)
	}
	marshalGit(t, repo, "config", "user.name", "Owner")
	marshalGit(t, repo, "config", "user.email", "owner@example.invalid")
	if _, err := s.Resume(ctx, "run"); err != nil {
		t.Fatalf("resume after setting an identity: %v", err)
	}
	d, err := s.Dispatch(ctx, "run", "a", "write file")
	if err != nil {
		t.Fatalf("dispatch after setting an identity: %v", err)
	}
	if _, err := s.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatalf("hand-in after setting an identity: %v", err)
	}
	if n := launches.Load(); n != 1 {
		t.Fatalf("worker launched %d times, want 1", n)
	}
}
