//go:build !governed

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

func failedAttemptFixture(t *testing.T, exhaust ...bool) (*MarshalService, string) {
	t.Helper()
	s, _, _ := parallelWaitFixture(t, 1)
	if len(exhaust) > 0 && exhaust[0] {
		run, rev, err := s.load(t.Context(), "par")
		if err != nil {
			t.Fatal(err)
		}
		run.Tasks[0].InfraRetries = infraRetryLimit
		if err := s.save(t.Context(), "par", run, rev); err != nil {
			t.Fatal(err)
		}
	}

	var failed string
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		if err := os.WriteFile(filepath.Join(req.Worktree, "a.txt"), []byte("failed attempt\n"), 0600); err != nil {
			return nil, err
		}
		if _, err := gitMarshal(ctx, req.Worktree, "add", "a.txt"); err != nil {
			return nil, err
		}
		if _, err := gitMarshal(ctx, req.Worktree, "commit", "-m", "attempt before failed hand-in"); err != nil {
			return nil, err
		}
		var err error
		failed, err = gitMarshal(ctx, req.Worktree, "rev-parse", "HEAD")
		if err != nil {
			return nil, err
		}
		return nil, errors.New("transport lost after import")
	}}
	d, err := s.Dispatch(t.Context(), "par", "a", "brief")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectHandIn(t.Context(), "par", d); err != nil {
		t.Fatal(err)
	}
	run, _, err := s.load(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	wantState := marshal.Returned
	if len(exhaust) > 0 && exhaust[0] {
		wantState = marshal.Escalated
	}
	if run.Tasks[0].FailedAttemptCommit != failed || run.Tasks[0].ResultCommit != "" || run.Tasks[0].State != wantState {
		t.Fatalf("failed identity not retained: %+v", run.Tasks[0])
	}
	return s, failed
}

func TestFailedAttemptRetryPreservesCommitAndLaunchesFromBase(t *testing.T) {
	s, failed := failedAttemptFixture(t, true)
	if err := s.RetryTask(t.Context(), "par", "a", true); err != nil {
		t.Fatal(err)
	}
	run, _, _ := s.load(t.Context(), "par")
	ref := returnedTaskRef("par", run.Tasks[0])
	base := run.Tasks[0].BaseCommit
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		head, err := gitMarshal(ctx, req.Worktree, "rev-parse", "HEAD")
		if err != nil {
			return nil, err
		}
		if head != base {
			return nil, errors.New("retry did not restart at admitted base")
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, "a.txt"), []byte("recovered\n"), 0600)
	}}
	d, err := s.Dispatch(t.Context(), "par", "a", "retry")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectHandIn(t.Context(), "par", d); err != nil {
		t.Fatal(err)
	}
	if got := marshalGit(t, s.Repository, "rev-parse", ref); got != failed {
		t.Fatalf("failed attempt lost: %s want %s", got, failed)
	}
}

func TestFailedAttemptRetryPreservesDirtyAndMovedWorktrees(t *testing.T) {
	for _, change := range []string{"dirty", "moved"} {
		t.Run(change, func(t *testing.T) {
			s, _ := failedAttemptFixture(t)
			run, _, _ := s.load(t.Context(), "par")
			dir := filepath.Join(s.Worktrees, taskArtifactID("par", run.Tasks[0]))
			if change == "dirty" {
				if err := os.WriteFile(filepath.Join(dir, "precious"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				marshalGit(t, dir, "reset", "--hard", run.BaseCommit)
			}
			before := marshalGit(t, dir, "rev-parse", "HEAD")
			if err := s.RetryTask(t.Context(), "par", "a", true); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Dispatch(t.Context(), "par", "a", "retry"); err == nil {
				t.Fatal("changed attempt was silently reset")
			}
			if got := marshalGit(t, dir, "rev-parse", "HEAD"); got != before {
				t.Fatal("refused retry moved branch")
			}
			if change == "dirty" {
				if data, err := os.ReadFile(filepath.Join(dir, "precious")); err != nil || string(data) != "keep" {
					t.Fatalf("lost dirty artifact: %q %v", data, err)
				}
			}
		})
	}
}

func TestProcess05IdentityAdmissionDoesNotConsumeInfraRetries(t *testing.T) {
	s, _, _ := parallelWaitFixture(t, 1)
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(context.Context, driver.Request) ([]marshal.CommandRecord, error) {
		return nil, errors.New("process 05: invalid Marshal task branch")
	}}
	run := executeUntil(t, s, "par", func(run marshal.Run) bool { return run.Pause != nil })
	if run.Tasks[0].InfraRetries != 0 || run.Tasks[0].State != marshal.Escalated || run.Pause == nil || !strings.Contains(run.Pause.Reason, "invalid Marshal task branch") {
		t.Fatalf("admission retried as infrastructure: %+v", run)
	}
}

func TestOneResumeRecoversAdvancedInterruptedWorker(t *testing.T) {
	s, _, _ := parallelWaitFixture(t, 1)
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		if err := os.WriteFile(filepath.Join(req.Worktree, "a.txt"), []byte("interrupted result\n"), 0600); err != nil {
			return nil, err
		}
		if _, err := gitMarshal(ctx, req.Worktree, "add", "a.txt"); err != nil {
			return nil, err
		}
		_, err := gitMarshal(ctx, req.Worktree, "commit", "-m", "before restart")
		return nil, errors.Join(err, errors.New("interrupted before hand-in"))
	}}
	dispatch, err := s.Dispatch(t.Context(), "par", "a", "brief")
	if err != nil {
		t.Fatal(err)
	}
	// The worker ran but its collection receipt was lost at process shutdown.
	if _, err := dispatch.Driver.Wait(t.Context(), dispatch.Handle); err == nil {
		t.Fatal("fixture worker should fail before hand-in")
	}
	dir := filepath.Join(s.Worktrees, taskArtifactID("par", mustSnapshot(t, s, "par").Tasks[0]))
	failed := marshalGit(t, dir, "rev-parse", "HEAD")
	reopened := *s // reconstruct service composition over the same durable store
	reopened.Drivers = map[string]driver.Driver{"worker": driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		return nil, os.WriteFile(filepath.Join(req.Worktree, "a.txt"), []byte("recovered\n"), 0600)
	}}}
	run, err := reopened.Resume(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	if run.Pause != nil || run.Tasks[0].State != marshal.Returned || run.Tasks[0].FailedAttemptCommit != failed {
		t.Fatalf("first resume did not recover interrupted worker: %+v", run)
	}
	ref := returnedTaskRef("par", run.Tasks[0])
	run, err = reopened.Execute(t.Context(), "par", func(marshal.Task, BriefContext) string { return "brief" }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.Tasks[0].State != marshal.Merged {
		t.Fatalf("second resume required to progress: %+v", run)
	}
	if got := marshalGit(t, reopened.Repository, "rev-parse", ref); got != failed {
		t.Fatal("interrupted result lost")
	}
}

func mustSnapshot(t *testing.T, s *MarshalService, id string) marshal.Run {
	t.Helper()
	run, err := s.Snapshot(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestInfraFailureRetainsDirtyAttemptWithActionablePause(t *testing.T) {
	s, _, _ := parallelWaitFixture(t, 1)
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		if err := os.WriteFile(filepath.Join(req.Worktree, "precious"), []byte("unfinished work"), 0600); err != nil {
			return nil, err
		}
		return nil, errors.New("provider disconnected")
	}}
	d, err := s.Dispatch(t.Context(), "par", "a", "brief")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectHandIn(t.Context(), "par", d); err != nil {
		t.Fatal(err)
	}
	run := mustSnapshot(t, s, "par")
	if run.State != marshal.AwaitingUser || run.Pause == nil || !strings.Contains(run.Pause.Reason, "dirty") || run.Tasks[0].State != marshal.Escalated {
		t.Fatalf("dirty failure lost recovery reason: %+v", run)
	}
	path := filepath.Join(s.Worktrees, taskArtifactID("par", run.Tasks[0]), "precious")
	if data, err := os.ReadFile(path); err != nil || string(data) != "unfinished work" {
		t.Fatalf("dirty attempt lost: %q %v", data, err)
	}
}
