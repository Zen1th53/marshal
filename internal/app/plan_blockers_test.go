package app

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/plan"
)

// A plan that is not ready names each failed structural check, never the
// old catch-all "task graph, team, or verification is incomplete".
func TestPlanStructureBlockersNameEachFailedCheck(t *testing.T) {
	empty := strings.Join(planStructureBlockers(plan.ExecutionPlan{}), "; ")
	for _, want := range []string{"task graph is empty", "team has no members", "verification plan has no obligations"} {
		if !strings.Contains(empty, want) {
			t.Fatalf("empty plan blockers %q lack %q", empty, want)
		}
	}
	tasks := []plan.Task{{ID: "a", Title: "a"}}
	graph, err := plan.BuildGraph(tasks)
	if err != nil {
		t.Fatal(err)
	}
	p := plan.ExecutionPlan{Tasks: tasks, Graph: graph, Team: plan.Team{Members: []plan.Member{{}}},
		Verification: plan.VerificationPlan{Obligations: []plan.VerificationObligation{{}}, Uncovered: []string{"README has the usage line"}}}
	got := strings.Join(planStructureBlockers(p), "; ")
	if !strings.Contains(got, "verification plan is incomplete: 1 Goal criteria have no covering task") || !strings.Contains(got, `"README has the usage line"`) || strings.Contains(got, "task graph") || strings.Contains(got, "team") {
		t.Fatalf("uncovered criterion blockers = %q", got)
	}
	p.Verification.Uncovered = nil
	if blockers := planStructureBlockers(p); len(blockers) != 0 {
		t.Fatalf("complete structure reported blockers: %v", blockers)
	}
	if report := planBlockerReport(nil, []string{" x ", "x", ""}); strings.Join(report, "|") != "x" {
		t.Fatalf("report = %v", report)
	}
	if report := planBlockerReport(); len(report) != 1 || report[0] == "" {
		t.Fatalf("empty report = %v", report)
	}
}
