package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/verification"
)

// verifiedDeliveryFixture is a one-task run merged and verified, waiting for
// the person's close, with the person's main checkout on the target branch.
func verifiedDeliveryFixture(t *testing.T) (*MarshalService, string) {
	t.Helper()
	s, repo := marshalUserCloseFixture(t, 1)
	ctx := t.Context()
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	run, err := s.Execute(ctx, "run", marshalBrief, nil)
	if err != nil || run.State != marshal.Verifying {
		t.Fatalf("execute: %s %v", run.State, err)
	}
	if got := marshalGit(t, repo, "symbolic-ref", "HEAD"); got != "refs/heads/main" {
		t.Fatalf("checkout is on %s, want main", got)
	}
	return s, repo
}

func marshalBranches(t *testing.T, repo string) []string {
	t.Helper()
	out := marshalGit(t, repo, "for-each-ref", "--format=%(refname)", "refs/heads/"+statepath.RefName+"/")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// The normal case: a clean main checkout on the target is fast-forwarded,
// files and all, and the run's merged worktrees and branches are removed.
func TestCloseFastForwardsCleanCheckoutAndCleansUp(t *testing.T) {
	s, repo := verifiedDeliveryFixture(t)
	ctx := t.Context()
	// The person's own branch and linked worktree must survive the cleanup.
	marshalGit(t, repo, "branch", "mine")
	own := filepath.Join(t.TempDir(), "own")
	marshalGit(t, repo, "worktree", "add", own, "mine")
	integration := marshalGit(t, repo, "rev-parse", "refs/heads/"+statepath.RefName+"/run/integration")
	if err := s.Close(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	if got := marshalGit(t, repo, "rev-parse", "HEAD"); got != integration {
		t.Fatalf("checkout HEAD = %s, want the delivered %s", got, integration)
	}
	if got := marshalGit(t, repo, "symbolic-ref", "HEAD"); got != "refs/heads/main" {
		t.Fatalf("checkout left on %s", got)
	}
	if status := marshalGit(t, repo, "status", "--porcelain"); status != "" {
		t.Fatalf("checkout not consistent after delivery: %q", status)
	}
	if _, err := os.Stat(filepath.Join(repo, "a.txt")); err != nil {
		t.Fatalf("delivered file missing: %v", err)
	}
	if left := marshalBranches(t, repo); len(left) != 0 {
		t.Fatalf("merged MARSHAL branches left: %v", left)
	}
	entries, _ := os.ReadDir(s.Worktrees)
	if len(entries) != 0 {
		t.Fatalf("run worktrees left: %v", entries)
	}
	list := marshalGit(t, repo, "worktree", "list", "--porcelain")
	if !strings.Contains(list, "branch refs/heads/mine") || strings.Count(list, "worktree ") != 2 {
		t.Fatalf("worktree list after cleanup:\n%s", list)
	}
	if marshalGit(t, repo, "rev-parse", "refs/"+statepath.RefName+"/run/pre-close") == "" {
		t.Fatal("undo checkpoint removed")
	}
}

// The real-user case: the checkout of the target holds uncommitted edits to
// the very files the run delivers. Nothing is touched, the stop says which
// files and what to do, and once they are stashed the same close delivers.
func TestCloseDirtyCheckoutStopsPlainlyThenDeliversAfterStash(t *testing.T) {
	s, repo := verifiedDeliveryFixture(t)
	ctx := t.Context()
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("my edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("my own a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := marshalGit(t, repo, "rev-parse", "HEAD")
	err := s.Close(ctx, "run")
	var stop *DeliveryStopError
	if !errors.As(err, &stop) {
		t.Fatalf("close error = %v, want a delivery stop", err)
	}
	if stop.Reason != "Your checkout of main has uncommitted changes (README.md, a.txt)." {
		t.Fatalf("reason = %q", stop.Reason)
	}
	if stop.Next != "Stash them (git stash -u) or undo them, then press Enter to finish delivering." || strings.Contains(err.Error(), "detach") {
		t.Fatalf("next = %q (%v)", stop.Next, err)
	}
	if got := marshalGit(t, repo, "rev-parse", "HEAD"); got != before {
		t.Fatalf("target moved to %s", got)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(got) != "my own a\n" {
		t.Fatalf("uncommitted file changed: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "README.md")); string(got) != "my edit\n" {
		t.Fatalf("uncommitted edit changed: %q", got)
	}
	run, err := s.Snapshot(ctx, "run")
	if err != nil || run.State != marshal.Verifying || run.Operation != nil {
		t.Fatalf("run after stop: %s op=%v %v", run.State, run.Operation, err)
	}
	// The stop is found before anything is recorded: no checkpoint, no
	// escalation for a refusal the person resolves.
	if _, err := gitMarshal(ctx, repo, "rev-parse", "--verify", "refs/"+statepath.RefName+"/run/pre-close"); err == nil {
		t.Fatal("a stopped close recorded a checkpoint")
	}
	marshalGit(t, repo, "stash", "-u")
	if err := s.Close(ctx, "run"); err != nil {
		t.Fatalf("close after stash: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(got) != "done\n" {
		t.Fatalf("delivered a.txt = %q", got)
	}
	if status := marshalGit(t, repo, "status", "--porcelain"); status != "" {
		t.Fatalf("checkout not consistent after delivery: %q", status)
	}
	if stash := marshalGit(t, repo, "stash", "list"); stash == "" {
		t.Fatal("the person's stash was dropped")
	}
}

// An untracked cache a test run leaves in the checkout is not the person's
// work and does not stop delivery.
func TestCloseIgnoresUntrackedCachesInCheckout(t *testing.T) {
	s, repo := verifiedDeliveryFixture(t)
	if err := os.MkdirAll(filepath.Join(repo, "__pycache__"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "__pycache__", "x.pyc"), []byte("cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "__pycache__", "x.pyc")); err != nil {
		t.Fatalf("cache removed: %v", err)
	}
}

// A refusal found inside the close effect (after the checkpoint) names the
// same plain reason in the run's escalation, not a generic refusal.
func TestCloseEffectStopNamesReasonInEscalation(t *testing.T) {
	s, repo := verifiedDeliveryFixture(t)
	ctx := t.Context()
	// Dirty the checkout after the precheck, as a concurrent edit would.
	s.requiredContextCloseEffect = func(_ context.Context, _ string, _ marshal.Run, _ *marshal.LifecycleOperation, effect func() error) error {
		if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("late edit\n"), 0o600); err != nil {
			return err
		}
		return effect()
	}
	err := s.Close(ctx, "run")
	var stop *DeliveryStopError
	if !errors.As(err, &stop) || stop.Reason != "Your checkout of main has uncommitted changes (README.md)." {
		t.Fatalf("close error = %v", err)
	}
	evs, err := s.Store.MarshalDecisions(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evs {
		if e.Type == events.EventTypeMarshalEscalated && e.Data["reason"] == stop.Reason {
			found = true
		}
	}
	if !found {
		t.Fatal("escalation does not name the stop's reason")
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "README.md")); string(got) != "late edit\n" {
		t.Fatalf("uncommitted edit changed: %q", got)
	}
}

// Under standing close authorization a dirty checkout stops Execute with the
// plain reason before the integrated result is verified again.
func TestExecuteAutoCloseStopsOnDirtyCheckoutBeforeVerifying(t *testing.T) {
	s, repo := marshalFixture(t, 1)
	ctx := t.Context()
	settings := marshal.DefaultSettings()
	settings.AcceptanceMode = marshal.AcceptMarshal
	if _, err := s.Store.SetMarshalSettings(ctx, s.ProjectID, settings, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	verify := s.Verify
	verified := 0
	s.Verify = func(ctx context.Context, run marshal.Run, head string) (verification.Session, verification.Binding, error) {
		verified++
		return verify(ctx, run, head)
	}
	_, err := s.Execute(ctx, "run", marshalBrief, nil)
	var stop *DeliveryStopError
	if !errors.As(err, &stop) || stop.Reason != "Your checkout of main has uncommitted changes (a.txt)." {
		t.Fatalf("execute error = %v", err)
	}
	if verified != 0 {
		t.Fatalf("verified %d times while the checkout was in the way", verified)
	}
	if err := os.Remove(filepath.Join(repo, "a.txt")); err != nil {
		t.Fatal(err)
	}
	run, err := s.Execute(ctx, "run", marshalBrief, nil)
	if err != nil || run.State != marshal.Closed {
		t.Fatalf("retry: %s %v", run.State, err)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(got) != "done\n" {
		t.Fatalf("delivered a.txt = %q", got)
	}
}

// Cleanup keeps what is not finished: an unmerged MARSHAL branch, a dirty
// run worktree, and anything of another run or of the person.
func TestCloseCleanupKeepsUnmergedAndDirty(t *testing.T) {
	s, repo := verifiedDeliveryFixture(t)
	ctx := t.Context()
	run, err := s.Snapshot(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	taskDir := filepath.Join(s.Worktrees, taskArtifactID("run", run.Tasks[0]))
	if err := os.WriteFile(filepath.Join(taskDir, "scratch.txt"), []byte("worker scratch"), 0o600); err != nil {
		t.Fatal(err)
	}
	// An unmerged attempt of this run, and a branch of another run.
	unmerged := filepath.Join(t.TempDir(), "side")
	marshalGit(t, repo, "worktree", "add", "--detach", unmerged, "HEAD")
	if err := os.WriteFile(filepath.Join(unmerged, "side.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, unmerged, "add", "side.txt")
	marshalGit(t, unmerged, "commit", "-m", "side")
	side := marshalGit(t, unmerged, "rev-parse", "HEAD")
	marshalGit(t, repo, "worktree", "remove", unmerged)
	marshalGit(t, repo, "update-ref", "refs/heads/"+statepath.RefName+"/run/a-attempt-1", side)
	marshalGit(t, repo, "update-ref", "refs/heads/"+statepath.RefName+"/other/a", marshalGit(t, repo, "rev-parse", "HEAD"))
	if err := s.Close(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	branches := strings.Join(marshalBranches(t, repo), " ")
	for _, keep := range []string{"/run/a-attempt-1", "/other/a", run.Tasks[0].Branch} {
		if !strings.Contains(branches, keep) {
			t.Fatalf("cleanup removed %s; left %s", keep, branches)
		}
	}
	if strings.Contains(branches, "/run/integration") {
		t.Fatalf("merged integration branch left: %s", branches)
	}
	if _, err := os.Stat(filepath.Join(taskDir, "scratch.txt")); err != nil {
		t.Fatalf("dirty run worktree removed: %v", err)
	}
}

// A run closed by a build that did not clean up is cleaned when the project
// opens, including its Process05 lane worktree; another task's lane stays.
func TestRecoverCleansUpClosedRunLeftovers(t *testing.T) {
	s, repo := verifiedDeliveryFixture(t)
	ctx := t.Context()
	run, err := s.Snapshot(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	result := run.Tasks[0].ResultCommit
	if err := s.Close(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	branchesRoot := filepath.Join(repo, statepath.DirName, "branches")
	lane := filepath.Join(branchesRoot, "TASK-lane-0123abcd")
	marshalGit(t, repo, "worktree", "add", "-b", statepath.RefName+"/lane-0123abcd/a", lane, result)
	otherLane := filepath.Join(branchesRoot, "TASK-lane-4567ef01")
	marshalGit(t, repo, "worktree", "add", "-b", statepath.RefName+"/lane-4567ef01/z", otherLane, result)
	// A fresh path: this process pinned the removed task worktree's identity.
	leftover := filepath.Join(s.Worktrees, "TASK-run-r1-a")
	marshalGit(t, repo, "worktree", "add", "-b", statepath.RefName+"/run/r1/a", leftover, result)
	if err := s.RecoverPendingOperations(ctx); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{lane, leftover} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s left: %v", gone, err)
		}
	}
	if _, err := os.Stat(otherLane); err != nil {
		t.Fatalf("another task's lane removed: %v", err)
	}
	branches := strings.Join(marshalBranches(t, repo), " ")
	if strings.Contains(branches, "lane-0123abcd") || strings.Contains(branches, "/run/r1/a") || !strings.Contains(branches, "lane-4567ef01/z") {
		t.Fatalf("branches after recovery cleanup: %s", branches)
	}
}

// deliveredCommit is the commit a closed run moved its target to, from its
// close event: the run's integration branch is cleaned up once delivered.
func deliveredCommit(t *testing.T, s *MarshalService, runID string) string {
	t.Helper()
	evs, err := s.Store.MarshalDecisions(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type == events.EventTypeMarshalRunClosed {
			if commit, _ := evs[i].Data["commit"].(string); commit != "" {
				return commit
			}
		}
	}
	t.Fatal("no close event")
	return ""
}
