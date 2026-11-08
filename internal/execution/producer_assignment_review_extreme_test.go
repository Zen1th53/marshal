//go:build !governed

package execution

import (
	"github.com/Zen1th53/marshal/internal/plan"
	"testing"
)

func TestExtremeExecutionFallbackCannotSelectIndependentAssignment(t *testing.T) {
	for _, match := range []string{"single", "role", "provider"} {
		t.Run(match, func(t *testing.T) {
			task := plan.Task{ID: "task", Role: string(plan.RoleDeveloper)}
			a := plan.Assignment{Harness: "claude-code", Provider: "claude", Independent: true}
			if match == "role" {
				a.Role = plan.RoleDeveloper
			}
			assignments := plan.AssignmentPlan{Assignments: []plan.Assignment{a}}
			if match != "single" {
				assignments.Assignments = append(assignments.Assignments, plan.Assignment{Harness: "codex", Role: plan.RoleQA, Provider: "codex"})
			}
			if got := assignedHarness(assignments, task, plan.Route{Provider: "claude"}); got != "" {
				t.Fatalf("%s fallback selected independent producer: %q", match, got)
			}
		})
	}
}
