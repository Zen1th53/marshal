package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
)

// assignPlanWorker gives the fixture's approved plan a producer assignment for
// task a, as a Process 04 plan has.
func assignPlanWorker(t *testing.T, s *MarshalService, harness string) {
	t.Helper()
	ctx := context.Background()
	run, rev, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		t.Fatal(err)
	}
	p.Assignments.Assignments = append(p.Assignments.Assignments, plan.Assignment{Role: plan.RoleDeveloper, Tasks: []string{"a"}, Harness: harness})
	if err = s.Store.SavePlan(ctx, p, p.Version); err != nil {
		t.Fatal(err)
	}
	run.PlanVersion = p.Version + 1
	if err = s.save(ctx, "run", run, rev); err != nil {
		t.Fatal(err)
	}
}

// The operator may switch a governed Process 05 task to another provider only
// in the private edition, and the switch is recorded with its previous worker
// and reason. In Community the runtime's own reassignment never switches; in
// Extreme the plan's worker is only a preference, so the Marshal may switch.
func TestMarshalOperatorProviderSwitchIsRecordedAndRuntimeCannotSwitch(t *testing.T) {
	ctx := context.Background()
	s := governedEscalatedFixture(t)
	run, _, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	planned := run.Tasks[0].Worker
	assignPlanWorker(t, s, planned)
	if flexibleWorkerModels {
		free := governedEscalatedFixture(t)
		assignPlanWorker(t, free, planned)
		if err := free.Reassign(ctx, "run", "a", "other"); err != nil {
			t.Fatalf("Marshal refused the Marshal's own provider switch: %v", err)
		}
	} else if err := s.Reassign(ctx, "run", "a", "other"); err == nil || !strings.Contains(err.Error(), "plan amendment") {
		t.Fatalf("the runtime switched a governed task's provider: %v", err)
	}
	err = s.ReassignByOperator(ctx, "run", "a", "other", "codex limit until Nov 4")
	if !governedProviderSwitchAllowed {
		if err == nil || !strings.Contains(err.Error(), "plan amendment") {
			t.Fatalf("Community switched a governed task's provider: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	run, _, err = s.load(ctx, "run")
	if err != nil || run.Tasks[0].Worker != "other" || run.Tasks[0].State != marshal.Reassigned {
		t.Fatalf("operator switch not applied: %+v %v", run.Tasks[0], err)
	}
	decisions, err := s.marshalDecisionsResolved(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	var recorded events.Event
	for _, event := range decisions {
		if event.Type == events.EventTypeMarshalTaskReassigned && event.TaskID == "a" {
			recorded = event
		}
	}
	if recorded.Data["provider_switch"] != true || recorded.Data["actor"] != "operator" || recorded.Data["previous_worker"] != planned || recorded.Data["planned_worker"] != planned || recorded.Data["reason"] != "codex limit until Nov 4" || recorded.Data["worker"] != "other" {
		t.Fatalf("switch audit record: %v", recorded.Data)
	}
}

// A cross-review never falls back to the worker's provider (or one skipped
// for its limit) in the private edition.
func TestCrossReviewProviderIsNeverTheWorkers(t *testing.T) {
	if p, err := crossReviewProvider([]string{"claude", "codex", "agy"}, []string{"claude"}, "claude", "a"); err != nil || p == "claude" {
		t.Fatalf("cross-review provider %q %v", p, err)
	}
	if p, err := crossReviewProvider([]string{"claude", "codex", "agy"}, []string{"claude", "codex"}, "claude", "a"); err != nil || p != "agy" {
		t.Fatalf("cross-review skipped provider: %q %v", p, err)
	}
	p, err := crossReviewProvider([]string{"claude", "codex"}, []string{"claude", "codex"}, "claude", "a")
	if independentCrossReviewRequired {
		if err == nil {
			t.Fatalf("the worker's provider %q reviewed its own hand-in", p)
		}
	} else if err != nil {
		t.Fatalf("Community single-provider fallback refused: %v", err)
	}
}

// The operator may move a governed task that is still queued to another
// provider in the private edition; it stays queued, so the runtime does not
// reassign it again. The runtime itself never moves a queued task.
func TestMarshalOperatorMovesQueuedGovernedTask(t *testing.T) {
	ctx := context.Background()
	s := governedEscalatedFixture(t)
	run, rev, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	planned := run.Tasks[0].Worker
	run.Tasks[0].State = marshal.Queued
	run.Tasks[0].EscalationReason = ""
	run.Tasks[0].ReturnsByAgent = nil
	run.Pause = nil
	run.State = marshal.Dispatching
	if err = s.save(ctx, "run", run, rev); err != nil {
		t.Fatal(err)
	}
	assignPlanWorker(t, s, planned)
	if err := s.Reassign(ctx, "run", "a", "other"); err == nil {
		t.Fatal("the runtime moved a queued task")
	}
	err = s.ReassignByOperator(ctx, "run", "a", "other", "codex limit until Nov 4")
	if !governedProviderSwitchAllowed {
		if err == nil {
			t.Fatal("Community moved a queued governed task")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	run, _, err = s.load(ctx, "run")
	if err != nil || run.Tasks[0].Worker != "other" || run.Tasks[0].State != marshal.Queued || len(run.Tasks[0].ReturnsByAgent) > 1 {
		t.Fatalf("queued task was not moved in place: %+v %v", run.Tasks[0], err)
	}
}
