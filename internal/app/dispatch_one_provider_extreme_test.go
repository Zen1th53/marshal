//go:build !governed

package app

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

// The live Mac demo after #340: Claude Code kept free as the final checker,
// so all four independent governed tasks went to codex. Once the plan is
// approved, dispatch starts every one of them on codex and none on Claude
// Code: the reservation and the provider admission do not hold a plan whose
// tasks all share one provider.
func TestApprovedPlanOnOneProviderDispatchesEveryIndependentTask(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _ := marshalFixture(t, 4)
	s.Gate = marshalTestGate(true)
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
	s.CheckerCandidates = func() []string { return []string{"claude"} }
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
