//go:build !governed

package app

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
)

func TestExtremeConservativeFileScopes(t *testing.T) {
	cases := []struct {
		a, b    []string
		overlap bool
	}{
		{[]string{"."}, []string{"src/a.go"}, true},
		{nil, []string{"a.go"}, true},
		{[]string{""}, []string{"a.go"}, true},
		{[]string{"src/**"}, []string{"docs/a.md"}, true},
		{[]string{"../a"}, []string{"b"}, true},
		{[]string{"src"}, []string{"src/a.go"}, true},
		{[]string{"src/a.go"}, []string{"src/b.go"}, false},
		{[]string{"a"}, []string{"ab"}, false},
	}
	for _, tc := range cases {
		if got := taskFilesOverlap(tc.a, tc.b); got != tc.overlap {
			t.Errorf("overlap(%v,%v) = %v", tc.a, tc.b, got)
		}
		if got := taskFilesOverlap(tc.b, tc.a); got != tc.overlap {
			t.Errorf("overlap(%v,%v) = %v", tc.b, tc.a, got)
		}
	}
}

func TestExtremeBoundGovernedAdmissionRemainsSerialized(t *testing.T) {
	s, _ := setupExtremeService(t, 2)
	run := marshal.Run{Process05Bound: true, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{
		{PlanTaskID: "a", Mode: marshal.Governed, Worker: "codex", Files: []string{"a"}, State: marshal.Queued},
		{PlanTaskID: "b", Mode: marshal.Governed, Worker: "claude", Files: []string{"b"}, State: marshal.Queued},
	}}
	for _, gate := range []marshal.CapabilityGate{nil, s.Gate} {
		if err := checkDispatchAdmission(gate, run, run.Tasks[1], 1); err == nil {
			t.Fatal("bound successor admitted before predecessor merged")
		}
		run.Tasks[0].State = marshal.Dispatched
		if err := checkDispatchAdmission(gate, run, run.Tasks[1], 1); err == nil {
			t.Fatal("parallel bound governed dispatch admitted")
		}
		run.Tasks[0].State = marshal.Merged
		if err := checkDispatchAdmission(gate, run, run.Tasks[1], 1); err != nil {
			t.Fatal(err)
		}
		run.Tasks[0].State = marshal.Queued
	}
	if !shouldStopAfterLaunch(run, run.Tasks[0]) {
		t.Fatal("bound launch did not stop ready batch")
	}
	run.Process05Bound = false
	if shouldStopAfterLaunch(run, run.Tasks[0]) {
		t.Fatal("independent launch stopped ready batch")
	}
}

func TestExtremeNilCapabilityGateRetainsStandardLimit(t *testing.T) {
	run := marshal.Run{Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "a", Worker: "codex", State: marshal.Dispatched, Files: []string{"a"}}}}
	candidate := marshal.Task{PlanTaskID: "b", Worker: "claude", Files: []string{"b"}}
	if err := checkDispatchAdmission(nil, run, candidate, 1); err == nil {
		t.Fatal("nil gate bypassed standard concurrency")
	}
	if can, _ := canLaunchCandidate(nil, run, candidate, []MarshalDispatch{{TaskID: "a"}}); can {
		t.Fatal("nil gate launched parallel tasks")
	}
}

func TestExtremeReturnedCollectionDoesNotReviewMissingHandIn(t *testing.T) {
	ctx := context.Background()
	s, _ := setupExtremeService(t, 1)
	if _, err := s.StartPlanning(ctx, "returned", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "returned"); err != nil {
		t.Fatal(err)
	}
	rec, err := s.Store.GetMarshalRun(ctx, s.ProjectID, "returned")
	if err != nil {
		t.Fatal(err)
	}
	rec.Value.Tasks[0].State = marshal.Returned
	if _, err = s.Store.SetMarshalRun(ctx, s.ProjectID, "returned", rec.Value, rec.Revision); err != nil {
		t.Fatal(err)
	}
	if err = s.reviewCollectedDispatch(ctx, "returned", "a"); err != nil {
		t.Fatalf("returned task was reviewed: %v", err)
	}
}

func TestExtremeInvalidCollectionRetriesBeforeReview(t *testing.T) {
	ctx := context.Background()
	s, _ := setupExtremeService(t, 2)
	fake := s.Model.(marshalFakeModel)
	draft := fake.draft
	draft.Tasks[1].DependsOn = nil
	draft.Plan.Tasks[1].DependsOn = nil
	graph, err := plan.BuildGraph(draft.Plan.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	draft.Plan.Graph = graph
	s.Model = marshalFakeModel{draft: draft, review: fake.review}
	var attempts, reviews atomic.Int32
	s.Drivers["worker"] = driver.Governed{Provider: "worker", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		if req.Task.PlanTaskID == "a" {
			if attempts.Add(1) == 1 {
				return nil, os.WriteFile(filepath.Join(req.Worktree, "outside.txt"), []byte("outside scope\n"), 0600)
			}
			if err := os.Remove(filepath.Join(req.Worktree, "outside.txt")); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, req.Task.PlanTaskID+".txt"), []byte("done\n"), 0600)
	}}
	s.CrossReview = func(_ context.Context, task marshal.Task, h marshal.HandIn, _ marshal.Control) (marshal.Review, string, error) {
		reviews.Add(1)
		return marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "second", EvidenceRefs: []string{"check:test -f " + task.PlanTaskID + ".txt"}}, "test", nil
	}
	if _, err := s.StartPlanning(ctx, "invalid-collection", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "invalid-collection"); err != nil {
		t.Fatal(err)
	}
	run, err := s.Execute(ctx, "invalid-collection", marshalBrief, nil)
	if err != nil {
		t.Fatalf("invalid hand-in interrupted retry: %v", err)
	}
	if attempts.Load() != 2 || reviews.Load() != 2 || run.Tasks[0].State != marshal.Merged || run.Tasks[1].State != marshal.Merged {
		t.Fatalf("attempts=%d reviews=%d tasks=%v", attempts.Load(), reviews.Load(), run.Tasks)
	}
}

// A task parked on its own approval keeps its lane open, so it counts against
// the global cap and its provider's limit; the parked task itself may continue.
func TestExtremeApprovalWaitHoldsWorkerSlot(t *testing.T) {
	s, _ := setupExtremeService(t, 2)
	settings := marshal.DefaultSettings()
	settings.GlobalCap = 2
	parked := marshal.Task{PlanTaskID: "a", Worker: "codex", Files: []string{"a"}, State: marshal.Queued, Process05WaitRunID: "lane-a", Process05WaitApprovalID: "app-a"}
	run := marshal.Run{Settings: settings, Tasks: []marshal.Task{
		parked,
		{PlanTaskID: "b", Worker: "claude", Files: []string{"b"}, State: marshal.Dispatched},
		{PlanTaskID: "c", Worker: "opencode", Files: []string{"c"}, State: marshal.Queued},
	}}
	if marshal.TierPolicy(s.Gate, run.Settings).Tier != marshal.Ultra {
		t.Skip("fixture gate is not ULTRA")
	}
	if can, _ := canLaunchCandidate(s.Gate, run, run.Tasks[2], nil); can {
		t.Fatal("a parked approval wait did not hold its slot; dispatch would exceed the lane capacity")
	}
	if can, _ := canLaunchCandidate(s.Gate, run, parked, nil); !can {
		t.Fatal("a parked task was blocked by its own slot")
	}
}
