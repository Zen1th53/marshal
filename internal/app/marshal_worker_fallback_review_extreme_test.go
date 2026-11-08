//go:build !governed

package app

import (
	"github.com/Zen1th53/marshal/internal/plan"
	"testing"
)

func TestReviewExtremeMarshalWorkerFallbackPreservesGovernedAliasAndIndependence(t *testing.T) {
	task := plan.Task{ID: "TASK-review", Role: string(plan.RoleDeveloper)}
	p := plan.ExecutionPlan{Assignments: plan.AssignmentPlan{Assignments: []plan.Assignment{{Role: plan.RoleDeveloper, Harness: "claude-code"}}}}
	if got, err := marshalPlanTaskWorker(p, task); err != nil || got != "claude" {
		t.Errorf("role/single fallback dispatched raw profile alias: %q %v", got, err)
	}
	p.Assignments.Assignments[0].Independent = true
	p.Assignments.Assignments[0].Role = plan.RoleQA
	p.Assignments.Assignments[0].Tasks = []string{task.ID}
	if got, err := marshalPlanTaskWorker(p, task); err == nil {
		t.Errorf("fallback selected independent review harness as producer: %q", got)
	}
	p.Assignments.Assignments = []plan.Assignment{{Role: plan.RoleDeveloper, Harness: "codex"}, {Role: plan.RoleDeveloper, Harness: "claude-code"}}
	if got, err := marshalPlanTaskWorker(p, task); err == nil {
		t.Errorf("ambiguous role-only worker admitted: %q", got)
	}
	p.Assignments.Assignments[1].Tasks = []string{task.ID}
	if got, err := marshalPlanTaskWorker(p, task); err != nil || got != "claude" {
		t.Errorf("exact assignment lost precedence over generic role: %q %v", got, err)
	}
}
