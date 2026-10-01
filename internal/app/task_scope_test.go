package app

import (
	"context"
	"testing"
)

func TestActivePlanScopeUsesCanonicalTaskIDs(t *testing.T) {
	r := runtimeForPlan(t)
	if _, err := r.ActivePlanScope(context.Background()); err == nil {
		t.Fatal("active scope without a plan was accepted")
	}
	p := canonicalDecisionPlan(t, r)
	scope, err := r.ActivePlanScope(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if scope.PlanID != p.ID || scope.PlanVersion != p.Version || len(scope.TaskIDs) != len(p.Tasks) || len(p.Tasks) == 0 {
		t.Fatalf("scope %+v for plan %s v%d with %d tasks", scope, p.ID, p.Version, len(p.Tasks))
	}
	for _, task := range p.Tasks {
		if !scope.TaskIDs[canonicalPlanTaskID(p.ID, p.Version, task.ID)] {
			t.Fatalf("plan task %s missing from scope", task.ID)
		}
		if scope.TaskIDs[task.ID] {
			t.Fatalf("display ID %s used as membership", task.ID)
		}
	}
}
