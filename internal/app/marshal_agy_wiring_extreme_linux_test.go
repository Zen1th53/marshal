//go:build !governed && linux

package app

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/project"
	"testing"
)

func TestAGYMarshalWiringUsesGovernedProcess05AndRequiresCurrentProfile(t *testing.T) {
	fixture, _ := marshalFixture(t, 1)
	r := &Runtime{store: fixture.Store, layout: project.Layout{Root: fixture.Repository, Worktrees: fixture.Worktrees}}
	s, err := r.MarshalWired(MarshalWiring{Provider: "codex", Approver: func(context.Context, string, string) (string, error) { return "operator", nil }})
	if err != nil {
		t.Fatal(err)
	}
	wired, ok := s.GovernedDrivers["agy"].(driver.Governed)
	if !ok || wired.Provider != "agy" || wired.Run == nil || wired.Check == nil {
		t.Fatal("real private AGY governed Process 05 driver is absent or incomplete")
	}
	if s.GovernedDrivers["antigravity"] != nil {
		t.Fatal("profile alias became a duplicate worker route")
	}
	if wired.Mode() != marshal.Governed {
		t.Fatal("AGY driver fell back to native mode")
	}
	s.InstalledVersion = func(context.Context, string) string { return "1.2.14" }
	if s.workerGovernance(t.Context(), "agy").Governed() {
		t.Fatal("wiring manufactured profile readiness")
	}
	engine := r.Execution().engine
	h, err := engine.GetHarness("agy")
	if err != nil || h.Name() != "agy" {
		t.Fatalf("canonical engine route: %v", err)
	}
	// A real runtime executor is installed: a fake/unbound task is refused by
	// the canonical task boundary, rather than delegated to a native CLI.
	task := execution.TaskExecution{TaskID: "unbound", AssignedModel: "gemini-3.8-flash-high"}
	pkg, err := execution.BuildConstraintPackage(planGoal(), task, plan.ExecutionPlan{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.Execute(t.Context(), task, pkg, fixture.Repository)
	if err == nil || !errors.Is(err, model.ErrConflict) {
		t.Fatalf("canonical unbound task refusal: %v", err)
	}
}
