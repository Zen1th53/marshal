package app

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

// A task that depends on another builds on its merged work, so its worker
// must find that work in the worktree it is given.
func TestMarshalDependentTaskStartsFromIntegrationHead(t *testing.T) {
	ctx := context.Background()
	s, repo := marshalFixture(t, 2)
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		if req.Task.PlanTaskID == "b" {
			if _, err := os.Stat(filepath.Join(req.Worktree, "a.txt")); err != nil {
				return nil, errors.New("dependency's work is missing from the worktree")
			}
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, req.Task.PlanTaskID+".txt"), []byte("done\n"), 0600)
	}}
	if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		d, err := s.Dispatch(ctx, "run", id, "write")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
			t.Fatalf("hand-in %s: %v", id, err)
		}
		if id == "b" {
			run, _, err := s.load(ctx, "run")
			if head := marshalGit(t, repo, "rev-parse", "refs/heads/"+statepath.RefName+"/run/integration"); err != nil || run.Tasks[1].BaseCommit != head {
				t.Fatalf("b's base = %s, want integration head %s (%v)", run.Tasks[1].BaseCommit, head, err)
			}
		}
		if v, err := s.Review(ctx, "run", id, knownCharge()); err != nil || v != marshal.VerdictAccept {
			t.Fatalf("review %s: %s %v", id, v, err)
		}
		if err := s.Merge(ctx, "run", id); err != nil {
			t.Fatal(err)
		}
	}
}

// A reassigned worker starts from the task's base, not from the work the
// previous worker had returned twice; that work stays reachable as evidence.
func TestMarshalReassignedWorkerStartsFresh(t *testing.T) {
	ctx := context.Background()
	s, repo := marshalFixture(t, 1)
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		return nil, os.WriteFile(filepath.Join(req.Worktree, "a.txt"), []byte("rejected\n"), 0600)
	}}
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	draft := s.Model.(marshalFakeModel).draft
	s.Model = marshalFakeModel{draft: draft, review: marshal.Review{Verdict: marshal.VerdictReturn, Reviewer: "marshal"}}
	var rejected string
	for attempt := 0; attempt < 2; attempt++ {
		d, err := s.Dispatch(ctx, "run", "a", "write")
		if err != nil {
			t.Fatal(err)
		}
		h, err := s.CollectHandIn(ctx, "run", d)
		if err != nil {
			t.Fatal(err)
		}
		rejected = h.ResultCommit
		if _, err = s.Review(ctx, "run", "a", knownCharge()); err != nil {
			t.Fatal(err)
		}
	}
	s.Drivers["other"] = driver.Governed{Provider: "other", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		if _, err := os.Stat(filepath.Join(req.Worktree, "a.txt")); err == nil {
			return nil, errors.New("the rejected work was handed to the new worker")
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, "a.txt"), []byte("fresh\n"), 0600)
	}}
	if err := s.Reassign(ctx, "run", "a", "other"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run", "a", "rework")
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.CollectHandIn(ctx, "run", d)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.load(ctx, "run")
	if err != nil || h.BaseCommit != run.BaseCommit {
		t.Fatalf("reassigned hand-in base = %s, want run base %s (%v)", h.BaseCommit, run.BaseCommit, err)
	}
	if got := marshalGit(t, repo, "rev-parse", "refs/"+statepath.RefName+"/run/returned/a/attempt-2"); got != rejected {
		t.Fatalf("returned attempt ref = %s, want %s", got, rejected)
	}
}

func TestStartedProcessExitPropagatesLaneReconciliationErrors(t *testing.T) {
	for _, cause := range []error{errors.New("lane storage unavailable"), context.Canceled, execution.ErrRunBlocked} {
		t.Run(cause.Error(), func(t *testing.T) {
			s, _ := marshalFixture(t, 1)
			run, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{})
			if err != nil {
				t.Fatal(err)
			}
			run, err = s.Approve(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			run.Process05Bound, run.Process05Parallel = true, true
			run.Tasks[0].Mode = marshal.Governed
			s.process05CloseOrphanLane = func(context.Context, marshal.Run, marshal.Task) error { return cause }
			exited, err := s.verifyStartedProcessExited(t.Context(), "run", run, run.Tasks[0])
			if exited || !errors.Is(err, cause) {
				t.Fatalf("exit=%v error=%v, want reconciliation error %v", exited, err, cause)
			}
			_, rev, err := s.load(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			held, _, err := s.reconcileStartedStaleLaunch(t.Context(), "run", run, rev, 0, "", errors.New("delivery base changed"))
			if err == nil || !held.Tasks[0].StaleProcessExitUnverified || held.Tasks[0].State != marshal.Dispatched || held.Tasks[0].QuarantinedRef != "" {
				t.Fatalf("failed reconciliation released reservation: %+v %v", held.Tasks[0], err)
			}
			s.process05CloseOrphanLane = func(context.Context, marshal.Run, marshal.Task) error { return nil }
			if exited, err := s.verifyStartedProcessExited(t.Context(), "run", held, held.Tasks[0]); !exited || err != nil {
				t.Fatalf("successful reconciliation: %v %v", exited, err)
			}
		})
	}
}
