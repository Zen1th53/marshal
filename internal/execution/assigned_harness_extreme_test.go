//go:build !governed

package execution

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/plan"
)

func TestExtremeAssignedHarnessNormalizesProfileAliasWithoutMutatingPlan(t *testing.T) {
	for _, match := range []string{"task", "role", "single", "provider"} {
		t.Run(match, func(t *testing.T) {
			a := plan.Assignment{Harness: "claude-code", Provider: "anthropic"}
			task := plan.Task{ID: "task", Role: "implementer"}
			switch match {
			case "task":
				a.Tasks = []string{task.ID}
				a.Role = "implementer"
				if got, bound := privateTaskAssignment(plan.AssignmentPlan{Assignments: []plan.Assignment{a}}, task); !bound || got != "claude" {
					t.Fatalf("exact assignment alias: got %q bound=%v", got, bound)
				}
			case "role":
				a.Role = "implementer"
			}
			assignments := plan.AssignmentPlan{Assignments: []plan.Assignment{a}}
			if match != "single" {
				assignments.Assignments = append(assignments.Assignments, plan.Assignment{Harness: "codex", Provider: "openai"})
			}
			if got := assignedHarness(assignments, task, plan.Route{Provider: "anthropic"}); got != "claude" {
				t.Fatalf("profile alias did not select registered governed worker: %q", got)
			}
			if assignments.Assignments[0].Harness != "claude-code" {
				t.Fatal("canonical profile evidence was mutated")
			}
		})
	}
}

func TestExtremePrivateTaskAssignmentKeepsIndependentAndUnknownHarnessFailClosed(t *testing.T) {
	task := plan.Task{ID: "task"}
	assignments := plan.AssignmentPlan{Assignments: []plan.Assignment{{Harness: "claude-code", Tasks: []string{"task"}, Independent: true}}}
	if _, bound := privateTaskAssignment(assignments, task); bound {
		t.Fatal("independent verifier became exact worker assignment")
	}
	assignments.Assignments[0].Independent = false
	assignments.Assignments[0].Harness = "unsupported"
	if got, bound := privateTaskAssignment(assignments, task); !bound || got != "unsupported" {
		t.Fatal("unknown harness acquired supported identity")
	}
}
