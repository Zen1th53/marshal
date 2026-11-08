//go:build !governed

package execution

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/plan"
	"testing"
)

func TestAntigravityGovernedHarnessRequiresRuntimeExecutor(t *testing.T) {
	h := NewAntigravityNativeHarness("")
	if h.Name() != "agy" {
		t.Fatal("canonical AGY route missing")
	}
	if r, e := h.Execute(t.Context(), TaskExecution{TaskID: "task"}, ConstraintPackage{}, "tree"); !errors.Is(e, ErrRunBlocked) || r.Success || len(r.EvidenceList) != 0 {
		t.Fatal("unbound AGY manufactured execution")
	}
	called := false
	h = NewAntigravityNativeHarnessWithExecutor("gemini-3.8-flash-high", func(_ context.Context, task TaskExecution, _ ConstraintPackage, tree string) (TaskResult, error) {
		called = true
		if task.TaskID != "task" || tree != "tree" {
			t.Fatal("binding lost")
		}
		return TaskResult{TaskID: task.TaskID, Success: true}, nil
	})
	if r, e := h.Execute(t.Context(), TaskExecution{TaskID: "task"}, ConstraintPackage{}, "tree"); e != nil || !called || !r.Success {
		t.Fatal("owned runtime callback not invoked")
	}
}

func TestAntigravityProfileAliasSelectsGovernedExecutor(t *testing.T) {
	a := plan.Assignment{Harness: "antigravity", Tasks: []string{"task"}}
	assignments := plan.AssignmentPlan{Assignments: []plan.Assignment{a}}
	task := plan.Task{ID: "task"}
	if got := assignedHarness(assignments, task, plan.Route{}); got != "agy" {
		t.Fatalf("profile alias selected %q", got)
	}
	if got, bound := privateTaskAssignment(assignments, task); !bound || got != "agy" {
		t.Fatal("private lane lost native profile alias")
	}
	if assignments.Assignments[0].Harness != "antigravity" {
		t.Fatal("native immutable assignment changed")
	}
}
