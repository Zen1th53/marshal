package app

import (
	"context"
	"errors"
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
			if head := marshalGit(t, repo, "rev-parse", "refs/heads/marshal/run/integration"); err != nil || run.Tasks[1].BaseCommit != head {
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
	if got := marshalGit(t, repo, "rev-parse", "refs/marshal/run/returned/a/attempt-2"); got != rejected {
		t.Fatalf("returned attempt ref = %s, want %s", got, rejected)
	}
}
