package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
)

func governedEscalatedFixture(t *testing.T) *MarshalService {
	t.Helper()
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	run, rev, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Process05Bound = true
	run.Tasks[0].Mode = marshal.Governed
	run.Tasks[0].State = marshal.Escalated
	run.Tasks[0].EscalationReason = "worker rework limit reached"
	run.Tasks[0].ReturnsByAgent = map[string]int{run.Tasks[0].Worker: 4}
	if err = s.save(ctx, "run", run, rev); err != nil {
		t.Fatal(err)
	}
	s.Drivers["other"] = driver.Governed{Provider: "other", Run: func(context.Context, driver.Request) ([]marshal.CommandRecord, error) { return nil, nil }}
	return s
}

// A Process 05 governed task stays on its approved plan's worker: another
// provider is refused with the amendment it needs, before any state changes.
func TestMarshalReassignRefusesGovernedProviderSwitch(t *testing.T) {
	ctx := context.Background()
	s := governedEscalatedFixture(t)
	err := s.Reassign(ctx, "run", "a", "other")
	if err == nil || !strings.Contains(err.Error(), "plan amendment") || !strings.Contains(err.Error(), "/marshal amend") {
		t.Fatalf("provider switch was not refused with the amendment it needs: %v", err)
	}
	run, _, err := s.load(ctx, "run")
	if err != nil || run.Tasks[0].State != marshal.Escalated || run.Tasks[0].Worker == "other" {
		t.Fatalf("refused reassignment changed the task: %+v %v", run.Tasks[0], err)
	}
}

func TestPlanProducerHarnessNamesOnlyTheProducer(t *testing.T) {
	p := plan.ExecutionPlan{}
	p.Assignments.Assignments = []plan.Assignment{
		{Role: plan.RoleDeveloper, Tasks: []string{"a"}, Harness: "codex", Independent: true},
		{Role: plan.RoleDeveloper, Tasks: []string{"a", "b"}, Harness: "claude-code"},
	}
	if got := planProducerHarness(p, "a"); got != "claude-code" {
		t.Fatalf("producer = %q, want claude-code", got)
	}
	if got := planProducerHarness(p, "c"); got != "" {
		t.Fatalf("unassigned task producer = %q", got)
	}
	if marshalHarnessName("claude") != marshalHarnessName(planProducerHarness(p, "b")) {
		t.Fatal("the plan's own worker does not match its harness name")
	}
}

// A dispatch refused for a harness mismatch ran no worker: it escalates to
// the person without counting a return against any worker.
func TestMarshalGovernedHarnessMismatchIsNotAReturn(t *testing.T) {
	ctx := context.Background()
	s := governedEscalatedFixture(t)
	run, rev, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Tasks[0].State = marshal.Dispatched
	run.Tasks[0].EscalationReason = ""
	run.Pause = nil
	if err = s.save(ctx, "run", run, rev); err != nil {
		t.Fatal(err)
	}
	wrapped := fmt.Errorf("worker failed: %s", errGovernedHarnessDiffers.Error())
	if !isGovernedHarnessMismatch(wrapped) || !isGovernedHarnessMismatch(fmt.Errorf("x: %w", errGovernedHarnessDiffers)) || isGovernedHarnessMismatch(errors.New("worker failed: exit 1")) {
		t.Fatal("harness mismatch recognition differs")
	}
	if err = s.escalateGovernedHarnessMismatch(ctx, "run", "a"); err != nil {
		t.Fatal(err)
	}
	run, _, err = s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	task := run.Tasks[0]
	if task.State != marshal.Escalated || task.EscalationReason != governedHarnessEscalation || isPermanentEscalationReason(task.EscalationReason) {
		t.Fatalf("mismatch was not a resolvable escalation: %s %q", task.State, task.EscalationReason)
	}
	total := 0
	for _, n := range task.ReturnsByAgent {
		total += n
	}
	if total != 4 || len(task.ReturnsByAgent) != 1 {
		t.Fatalf("mismatch counted as a worker return: %v", task.ReturnsByAgent)
	}
	if run.Pause == nil {
		t.Fatal("the run did not wait for the person")
	}
}
