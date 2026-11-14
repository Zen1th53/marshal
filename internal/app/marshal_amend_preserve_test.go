package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
)

// cloneMarshalDraft copies the task slices an amendment fixture edits.
func cloneMarshalDraft(d MarshalDraft) MarshalDraft {
	d.Tasks = slices.Clone(d.Tasks)
	d.Plan.Tasks = slices.Clone(d.Plan.Tasks)
	return d
}

// A major amendment that changes one task must keep merged work merged and
// keep the approved budget contract: the model's amendment draft carries no
// budget, and a plan without MaxTasks is refused by the Process 05 gate.
func TestMajorAmendmentKeepsMergedTasksAndBudget(t *testing.T) {
	ctx := t.Context()
	s, _ := marshalFixture(t, 2)
	fake := s.Model.(marshalFakeModel)
	approvedBudget := plan.Budget{MaxTasks: 2, RequiresCheckIn: true, Reason: "pause partway"}
	fake.draft.Plan.Budget = approvedBudget
	s.Model = fake
	if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Review(ctx, "run", "a", knownCharge()); err != nil {
		t.Fatal(err)
	}
	if err = s.Merge(ctx, "run", "a"); err != nil {
		t.Fatal(err)
	}
	before, _, err := s.load(ctx, "run")
	if err != nil || before.Tasks[0].State != marshal.Merged {
		t.Fatalf("fixture: %+v %v", before.Tasks, err)
	}

	changed := cloneMarshalDraft(fake.draft)
	changed.Plan.Budget = plan.Budget{}
	changed.Tasks[1].Instructions = "write b.txt with the new wording"
	changed.Plan.Tasks[1].Instructions = changed.Tasks[1].Instructions
	s.Model = marshalFakeModel{draft: fake.draft, review: fake.review, amend: changed}
	run, err := s.Amend(ctx, "run", "b reword the file")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != marshal.Drafting {
		t.Fatalf("amendment was not major: %s", run.State)
	}
	a, b := run.Tasks[0], run.Tasks[1]
	if a.State != marshal.Merged || a.ResultCommit != before.Tasks[0].ResultCommit || a.Branch != before.Tasks[0].Branch {
		t.Fatalf("merged task was reset by the amendment: %+v", a)
	}
	if b.State != marshal.Queued || b.Instructions != changed.Tasks[1].Instructions {
		t.Fatalf("amended task not queued with its change: %+v", b)
	}
	p, err := s.Store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		t.Fatal(err)
	}
	if p.Budget != approvedBudget {
		t.Fatalf("amended plan lost its budget contract: %+v", p.Budget)
	}
	if _, err = s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	approved, err := s.Store.GetPlan(ctx, run.PlanID, run.PlanVersion+1)
	if err != nil || approved.Budget.MaxTasks != 2 {
		t.Fatalf("approved amended plan budget: %+v %v", approved.Budget, err)
	}
	if _, err = s.Dispatch(ctx, "run", "a", "write"); err == nil {
		t.Fatal("merged task was dispatched again")
	}
	db, err := s.Dispatch(ctx, "run", "b", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(ctx, "run", db); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Review(ctx, "run", "b", knownCharge()); err != nil {
		t.Fatal(err)
	}
	if err = s.Merge(ctx, "run", "b"); err != nil {
		t.Fatal(err)
	}
	final, _, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	integration := filepath.Join(s.Worktrees, integrationTaskID("run", final))
	for _, file := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(integration, file)); err != nil {
			t.Fatalf("integration lost %s after the amendment: %v", file, err)
		}
	}
}

func TestAmendedPlanBudgetGrowsForAddedTasks(t *testing.T) {
	current := plan.Budget{MaxTasks: 6, RequiresCheckIn: true, Reason: "r"}
	if got := amendedPlanBudget(current, plan.Budget{}, 6); got != current {
		t.Fatalf("inherit: %+v", got)
	}
	if got := amendedPlanBudget(current, plan.Budget{}, 7); got.MaxTasks != 7 || !got.RequiresCheckIn {
		t.Fatalf("grow: %+v", got)
	}
	proposed := plan.Budget{MaxTasks: 9}
	if got := amendedPlanBudget(current, proposed, 7); got != proposed {
		t.Fatalf("explicit: %+v", got)
	}
}

// A Process 05 run takes routes and governed assignments only from the
// approved plan; an amendment adding a task is refused before approval with
// the working command instead of failing every worker at the entry gate.
func TestProcess05AmendmentRefusesUnroutedNewTask(t *testing.T) {
	current := plan.ExecutionPlan{Routes: map[string]plan.Route{"a": {Provider: "claude"}}}
	run := marshal.Run{Process05Bound: true}
	next := MarshalDraft{Plan: plan.ExecutionPlan{Tasks: []plan.Task{{ID: "a"}}}}
	if err := validateAmendRoutes(run, current, next); err != nil {
		t.Fatal(err)
	}
	next.Plan.Tasks = append(next.Plan.Tasks, plan.Task{ID: "extra"})
	err := validateAmendRoutes(run, current, next)
	if err == nil || !strings.Contains(err.Error(), "/marshal amend <task-id> <change>") {
		t.Fatalf("unrouted task: %v", err)
	}
	run.Process05Bound = false
	if err := validateAmendRoutes(run, current, next); err != nil {
		t.Fatalf("legacy run: %v", err)
	}
}

func TestAmendPacketMissingTargetNamesFormat(t *testing.T) {
	run := marshal.Run{Tasks: []marshal.Task{{PlanTaskID: "live-voice"}, {PlanTaskID: "regress-ui"}}}
	_, _, err := marshalAmendPacket(run, "add ultron/wake to scope")
	if err == nil || !strings.Contains(err.Error(), "<task-id> <change>") || !strings.Contains(err.Error(), "live-voice, regress-ui") {
		t.Fatalf("missing target: %v", err)
	}
}
