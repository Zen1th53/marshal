package plan_test

import (
	"reflect"
	"testing"

	"github.com/Zen1th53/marshal/internal/plan"
)

func amendFixture(t *testing.T) plan.ExecutionPlan {
	t.Helper()
	p := readyPlan(t)
	p.Tasks[0].Paths = []string{"internal/api/cache.go", "internal/api/store.go"}
	p.Graph, _ = plan.BuildGraph(p.Tasks)
	p.Checks = map[string][]string{"implement": {"go test ./internal/api"}, "test": {"go test ./..."}}
	p.Assignments.Assignments = []plan.Assignment{{Role: plan.RoleDeveloper, Tasks: []string{"implement", "test"}}}
	approved, err := p.Approve(planTime)
	if err != nil {
		t.Fatal(err)
	}
	return approved
}

func splitFixture(p plan.ExecutionPlan) plan.ExecutionPlan {
	next := p
	next.Tasks = append([]plan.Task(nil), p.Tasks...)
	next.Tasks[0].Paths = []string{"internal/api/cache.go"}
	next.Tasks = append(next.Tasks, plan.Task{ID: "implement-store", Title: "store cache", Criteria: []string{"responses are cached"}, Paths: []string{"internal/api/store.go"}, Mutating: true})
	next.ParentTaskIDs = map[string]string{"implement-store": "implement"}
	next.Checks = map[string][]string{"implement": {"go test ./internal/api"}, "implement-store": {"go test ./internal/api"}, "test": {"go test ./..."}}
	next.Routes = map[string]plan.Route{}
	for id, route := range p.Routes {
		next.Routes[id] = route
	}
	next.Routes["implement-store"] = p.Routes["implement"]
	next.Assignments.Assignments = []plan.Assignment{{Role: plan.RoleDeveloper, Tasks: []string{"implement", "implement-store", "test"}}}
	return next
}

func TestM03ScopedSplitRetainsApprovalAndHandoff(t *testing.T) {
	p := amendFixture(t)
	next := splitFixture(p)
	amended, err := p.AmendScoped(p.Version, "split implementation", next)
	if err != nil {
		t.Fatal(err)
	}
	if amended.State != plan.StateApproved || amended.Version != p.Version+1 || amended.RevisionReason != "split implementation" || amended.Supersedes != p.Version {
		t.Fatal("approval or revision metadata lost")
	}
	if amended.ApprovalScopeDigest == amended.Graph.Digest {
		t.Fatal("approval and graph digests match")
	}
	if _, err := plan.PrepareHandoff(amended, confirmedGoal(), testProject, planTime); err != nil {
		t.Fatal(err)
	}
}

func TestM03ScopeExpansionAndRemovalRejected(t *testing.T) {
	p := amendFixture(t)
	tests := map[string]func(*plan.ExecutionPlan){
		"criterion": func(n *plan.ExecutionPlan) { n.Tasks[0].Criteria = nil },
		"file":      func(n *plan.ExecutionPlan) { n.Tasks[0].Paths = append(n.Tasks[0].Paths, "outside.go") },
		"budget":    func(n *plan.ExecutionPlan) { n.Budget.MaxTasks++ },
		"goal":      func(n *plan.ExecutionPlan) { n.Goal.Revision++ },
		"checks": func(n *plan.ExecutionPlan) {
			n.Checks = map[string][]string{"implement": {"true"}, "test": {"go test ./..."}}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			next := p
			next.Tasks = append([]plan.Task(nil), p.Tasks...)
			mutate(&next)
			got, err := p.AmendScoped(p.Version, "change", next)
			if err == nil || !reflect.DeepEqual(got, p) {
				t.Fatal("change was accepted or original plan changed")
			}
		})
	}
}

func TestM03RedistributionAcrossTasksRejected(t *testing.T) {
	p := amendFixture(t)
	for _, field := range []string{"criterion", "file"} {
		t.Run(field, func(t *testing.T) {
			next := p
			next.Tasks = append([]plan.Task(nil), p.Tasks...)
			if field == "criterion" {
				next.Tasks[0].Criteria = nil
				next.Tasks[1].Criteria = append(next.Tasks[1].Criteria, "responses are cached")
			} else {
				next.Tasks[0].Paths = next.Tasks[0].Paths[:1]
				next.Tasks[1].Paths = []string{"internal/api/store.go"}
			}
			if _, err := p.AmendScoped(p.Version, "redistribute", next); err == nil {
				t.Fatal("redistribution accepted")
			}
		})
	}
}

func TestM03SplitRequiresRouteAndAssignment(t *testing.T) {
	p := amendFixture(t)
	for _, field := range []string{"route", "assignment"} {
		t.Run(field, func(t *testing.T) {
			next := splitFixture(p)
			if field == "route" {
				delete(next.Routes, "implement-store")
			} else {
				next.Assignments.Assignments[0].Tasks = []string{"implement", "test"}
			}
			if _, err := p.AmendScoped(p.Version, "split", next); err == nil {
				t.Fatal("incomplete split accepted")
			}
		})
	}
}

func TestM03RevisionStillWithdrawsApproval(t *testing.T) {
	p := amendFixture(t)
	revised, err := p.Revise(p.Version, "change scope", planTime)
	if err != nil || revised.State != plan.StateDraft {
		t.Fatal("revision retained approval", err)
	}
}

func TestM03ConcurrentAmendmentRejectsStaleVersion(t *testing.T) {
	p := amendFixture(t)
	first, err := p.AmendScoped(p.Version, "first", splitFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.AmendScoped(p.Version, "second", splitFixture(first)); err == nil {
		t.Fatal("stale amendment accepted")
	}
}

func TestM03ReorderAndRouteSwapStayScoped(t *testing.T) {
	p := amendFixture(t)
	next := p
	next.Tasks = append([]plan.Task(nil), p.Tasks...)
	next.Tasks[0], next.Tasks[1] = next.Tasks[1], next.Tasks[0]
	next.Routes = map[string]plan.Route{}
	for id, route := range p.Routes {
		next.Routes[id] = route
	}
	route := next.Routes["implement"]
	route.Provider = "claude"
	next.Routes["implement"] = route
	amended, err := p.AmendScoped(p.Version, "reorder and reassign", next)
	if err != nil || amended.Routes["implement"].Provider != "claude" {
		t.Fatal("scoped reorder or route swap failed", err)
	}
}
