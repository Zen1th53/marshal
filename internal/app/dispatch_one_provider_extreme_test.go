//go:build !governed

package app

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
)

// countingDriver records each launch, so a test can prove a worker never started.
type countingDriver struct {
	extremeMockDriver
	launches int
}

func (d *countingDriver) Launch(ctx context.Context, req driver.Request) (*driver.Handle, error) {
	d.launches++
	return d.extremeMockDriver.Launch(ctx, req)
}

// The live Mac demo: all four independent governed tasks went to codex.
// Once the plan is approved, dispatch starts every one of them on codex and
// none on Claude Code: the provider admission does not hold a plan whose
// tasks all share one provider.
func TestApprovedPlanOnOneProviderDispatchesEveryIndependentTask(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _ := marshalFixture(t, 4)
	s.Gate = marshalTestGate(true)
	model := s.Model.(marshalFakeModel)
	for i := range model.draft.Tasks {
		model.draft.Tasks[i].DependsOn = nil
		model.draft.Plan.Tasks[i].DependsOn = nil
	}
	model.draft.Plan.Graph, _ = plan.BuildGraph(model.draft.Plan.Tasks)
	s.Model = model
	if _, err := s.StartPlanning(t.Context(), "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Tier = marshal.Ultra
	for i := range run.Tasks {
		run.Tasks[i].Worker = "codex"
		run.Tasks[i].Mode = marshal.Governed
		run.Tasks[i].DependsOn = nil
	}
	if err := s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	s.QuotaLeft = func(provider string) (int, bool) {
		if provider == "codex" {
			return 99, true
		}
		return 0, false
	}
	codex := &countingDriver{extremeMockDriver: extremeMockDriver{mode: marshal.Governed}}
	claude := &countingDriver{extremeMockDriver: extremeMockDriver{mode: marshal.Governed}}
	s.GovernedDrivers = map[string]driver.Driver{"codex": codex, "claude": claude}
	s.Drivers = s.GovernedDrivers
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	approved, _, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	launched, err := s.dispatchReady(t.Context(), "run", approved, marshalBrief)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(launched) != 4 || codex.launches != 4 || claude.launches != 0 {
		t.Fatalf("dispatched %d (codex %d, claude %d), want all 4 on codex", len(launched), codex.launches, claude.launches)
	}
	after, _, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if after.Pause != nil {
		t.Fatalf("dispatch paused the run: %+v", after.Pause)
	}
	for _, task := range after.Tasks {
		if task.State == marshal.Queued {
			t.Fatalf("task %s left queued: %+v", task.PlanTaskID, after.Tasks)
		}
	}
}

// The live run's own shape: Standard tier (Tier ""). Standard runs one task
// at a time: the first codex task starts and nothing pauses.
func TestApprovedStandardPlanOnOneProviderStartsFirstTask(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _ := marshalFixture(t, 4)
	if _, err := s.StartPlanning(t.Context(), "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	for i := range run.Tasks {
		run.Tasks[i].Worker = "codex"
		run.Tasks[i].Mode = marshal.Governed
		run.Tasks[i].DependsOn = nil
	}
	if err := s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	codex := &countingDriver{extremeMockDriver: extremeMockDriver{mode: marshal.Governed}}
	claude := &countingDriver{extremeMockDriver: extremeMockDriver{mode: marshal.Governed}}
	s.GovernedDrivers = map[string]driver.Driver{"codex": codex, "claude": claude}
	s.Drivers = s.GovernedDrivers
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	approved, _, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	launched, err := s.dispatchReady(t.Context(), "run", approved, marshalBrief)
	if err != nil || len(launched) != 1 || codex.launches != 1 || claude.launches != 0 {
		t.Fatalf("dispatched %d (codex %d, claude %d), err %v: want the first task on codex", len(launched), codex.launches, claude.launches, err)
	}
	after, _, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if after.Pause != nil {
		t.Fatalf("dispatch paused the run: %+v", after.Pause)
	}
}
