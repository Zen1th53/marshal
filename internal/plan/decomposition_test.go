package plan_test

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/plan"
)

func TestManyCriteriaInOneTaskWarnsMarshalWithoutBlocking(t *testing.T) {
	goal := confirmedGoal()
	goal.SuccessCriteria = []string{"fix auth", "fix diagnostics", "fix planning", "test changes"}
	tasks := []plan.Task{{ID: "all", Title: "all fixes", Criteria: goal.SuccessCriteria}}
	p := buildPlan(t, plan.BuildRequest{Goal: goal, ProjectID: testProject, Tasks: tasks, Assessment: routineAssessment(), Candidates: governedCandidates()})
	if p.State != plan.StateReady || len(p.MarshalWarnings) != 1 {
		t.Fatalf("warning blocked or absent: %s %v", p.State, p.MarshalWarnings)
	}
	if !strings.Contains(p.MarshalWarnings[0], "do not ask the owner") || !strings.Contains(p.MarshalWarnings[0], "explicit dependencies") {
		t.Fatal(p.MarshalWarnings)
	}
	if strings.Contains(plan.Summarise(p), "Planning check for the Marshal") {
		t.Fatal("planning advice leaked into the owner's plan summary")
	}
	if len(plan.DecompositionWarnings(goal, []plan.Task{{ID: "one"}, {ID: "two"}})) != 0 || len(plan.DecompositionWarnings(goal, nil)) != 0 {
		t.Fatal("non-monolithic plan warned")
	}
	goal.SuccessCriteria = goal.SuccessCriteria[:3]
	if len(plan.DecompositionWarnings(goal, tasks)) != 0 {
		t.Fatal("small goal warned")
	}
}
