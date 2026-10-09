package marshal

import (
	"slices"
	"testing"
	"time"
)

func TestStateTransitions(t *testing.T) {
	for from := range runMoves {
		for _, to := range []RunState{Drafting, Approved, Dispatching, Reviewing, Merging, Verifying, Closed, AwaitingUser, "invalid"} {
			err := TransitionRun(from, to)
			if (err == nil) != slices.Contains(runMoves[from], to) {
				t.Fatalf("run %s -> %s", from, to)
			}
		}
	}
	for from := range taskMoves {
		for _, to := range []TaskState{Queued, Dispatched, HandedIn, Accepted, Returned, Reassigned, Escalated, Merged, "invalid"} {
			err := TransitionTask(from, to)
			if (err == nil) != slices.Contains(taskMoves[from], to) {
				t.Fatalf("task %s -> %s", from, to)
			}
		}
	}
}
func TestReworkPolicy(t *testing.T) {
	for n, want := range map[int]Verdict{1: VerdictReturn, 2: VerdictReturn, 3: VerdictReassign} {
		task := Task{Worker: "a", ReturnsByAgent: map[string]int{"a": n}}
		if got := NextAfterReturn(task, 2); got != want {
			t.Fatalf("%d: %s", n, got)
		}
	}
	task := Task{Worker: "b", ReturnsByAgent: map[string]int{"a": 3, "b": 0}}
	if NextAfterReturn(task, 2) != VerdictReturn {
		t.Fatal("reassigned worker has not failed")
	}
	task.ReturnsByAgent["b"] = 1
	if NextAfterReturn(task, 2) != VerdictEscalate {
		t.Fatal("expected escalation")
	}
}
func TestFailedCheckIsValidHandIn(t *testing.T) {
	task := fixture().Tasks[0]
	h := HandIn{ResultCommit: "abc", CheckResults: []CheckResult{{Command: "go test", Criteria: []string{"one", "two"}, ResultCommit: "abc", Passed: false}}}
	if err := ValidateHandIn(task, h); err != nil {
		t.Fatal(err)
	}
	met, total, failing := CriteriaMet(task, h)
	if met != 0 || total != 2 || !slices.Equal(failing, []string{"one", "two"}) {
		t.Fatalf("met=%d total=%d failing=%v", met, total, failing)
	}
	h.CheckResults = nil
	if ValidateHandIn(task, h) == nil {
		t.Fatal("missing result must be invalid")
	}
}
func fixture() Run {
	return Run{GoalBinding: "g", ApprovalScopeDigest: "d", Tasks: []Task{{PlanTaskID: "a", Mode: Native, Files: []string{"a.go", "b.go"}, Criteria: []string{"one", "two"}, Checks: []Check{{"go test", []string{"one", "two"}}}}, {PlanTaskID: "b", Mode: Native, Files: []string{"c.go"}, Criteria: []string{"three"}, DependsOn: []string{"a"}, Checks: []Check{{"go vet", []string{"three"}}}}}}
}
func TestAmendmentClassification(t *testing.T) {
	before := fixture()
	after := fixture()
	if ClassifyAmendment(before, after) != Scoped {
		t.Fatal("identity")
	}
	after.Tasks[0].PlanTaskID = "a1"
	after.Tasks[0].ParentID = "a"
	after.Tasks = append(after.Tasks, Task{PlanTaskID: "a2", ParentID: "a", Mode: Native, Files: []string{"b.go"}, Criteria: []string{"two"}, Checks: before.Tasks[0].Checks})
	after.Tasks[0].Files = []string{"a.go"}
	after.Tasks[0].Criteria = []string{"one"}
	if ClassifyAmendment(before, after) != Scoped {
		t.Fatal("split")
	}
	cases := map[string]func(*Run){"move criterion": func(r *Run) {
		r.Tasks[0].Criteria = []string{"two"}
		r.Tasks[1].Criteria = append(r.Tasks[1].Criteria, "one")
	}, "move file": func(r *Run) { r.Tasks[0].Files = []string{"b.go"}; r.Tasks[1].Files = append(r.Tasks[1].Files, "a.go") }, "add file": func(r *Run) { r.Tasks[0].Files = append(r.Tasks[0].Files, "new.go") }, "budget": func(r *Run) { r.Budget.Tokens.Plan = 1 }, "goal": func(r *Run) { r.GoalBinding = "changed" }}
	for name, change := range cases {
		r := fixture()
		change(&r)
		if ClassifyAmendment(before, r) != Major {
			t.Fatal(name)
		}
	}
}
func TestChecksChangeIsMajor(t *testing.T) {
	before := fixture()
	after := fixture()
	after.Tasks[0].Checks = nil
	if ClassifyAmendment(before, after) != Major {
		t.Fatal("removed checks")
	}
	after = fixture()
	after.Tasks[0].Checks[0].Command = "other"
	if ClassifyAmendment(before, after) != Major {
		t.Fatal("changed checks")
	}
}
func TestSelfReview(t *testing.T) {
	if CheckReviewer("worker", "worker") == nil || CheckReviewer("marshal", "marshal") == nil || CheckReviewer("worker", "marshal") != nil {
		t.Fatal("reviewer check")
	}
}
func TestHandInScopeAndEvidence(t *testing.T) {
	task := fixture().Tasks[0]
	h := HandIn{ResultCommit: "abc", FilesTouched: []string{"a.go"}, CheckResults: []CheckResult{{Command: "go test", Criteria: []string{"one", "two"}, Passed: true, ResultCommit: "abc"}}}
	if ValidateHandIn(task, h) != nil {
		t.Fatal("valid hand-in")
	}
	h.FilesTouched = append(h.FilesTouched, "outside.go")
	if ValidateHandIn(task, h) == nil {
		t.Fatal("out of scope")
	}
	h.FilesTouched = nil
	h.ResultCommit = ""
	if ValidateHandIn(task, h) == nil {
		t.Fatal("missing commit")
	}
	h.ResultCommit = "abc"
	h.CheckResults = nil
	if ValidateHandIn(task, h) == nil {
		t.Fatal("missing check")
	}
}
func TestSettingsDefaultsAndEnums(t *testing.T) {
	d := DefaultSettings()
	if d.ExecutionRights != RightsReadOnly || d.AcceptanceMode != AcceptMarshalThenUser || d.ReworkLimit != 2 || d.UltraConcurrency != 3 || d.Validate() != nil {
		t.Fatal(d)
	}
	d.ExecutionRights = "unknown"
	if d.Validate() == nil {
		t.Fatal("rights")
	}
	d = DefaultSettings()
	d.AcceptanceMode = "unknown"
	if d.Validate() == nil {
		t.Fatal("mode")
	}
}
func TestBudgetUnknownAndCeilings(t *testing.T) {
	b := Budget{Tokens: Ceiling{10, 20}, WallTime: Ceiling{10, 20}, Money: Ceiling{10, 20}}
	task := Charge{Tokens: Amount{Known: false}, WallTime: 11 * time.Second, Money: Amount{11, true}}
	plan := Charge{Tokens: Amount{21, true}, WallTime: 21 * time.Second, Money: Amount{21, true}}
	r := CheckBudget(b, task, plan)
	if !slices.Contains(r.Unknown, "tokens") || slices.Contains(r.TaskExceeded, "tokens") || !slices.Contains(r.PlanExceeded, "tokens") || len(r.TaskExceeded) != 2 || len(r.PlanExceeded) != 3 {
		t.Fatal(r)
	}
}
func TestBudgetWallTimeBoundary(t *testing.T) {
	b := Budget{WallTime: Ceiling{Task: 10, Plan: 20}}
	task := Charge{WallTime: 10 * time.Second}
	plan := Charge{WallTime: 20 * time.Second}
	if r := CheckBudget(b, task, plan); len(r.TaskExceeded) != 0 || len(r.PlanExceeded) != 0 {
		t.Fatal(r)
	}
	task.WallTime = 11 * time.Second
	plan.WallTime = 21 * time.Second
	if r := CheckBudget(b, task, plan); !slices.Contains(r.TaskExceeded, "wall_time") || !slices.Contains(r.PlanExceeded, "wall_time") {
		t.Fatal(r)
	}
}
func TestCloseAuthorizationDigest(t *testing.T) {
	r := fixture()
	r.Repository, r.BaseCommit, r.TargetRef = "repo", "base", "refs/heads/main"
	r.CloseAuthorization = &CloseAuthorization{User: "user", ApprovalScopeDigest: "d", Repository: "repo", BaseCommit: "base", TargetRef: "refs/heads/main"}
	if !r.ValidCloseAuthorization() {
		t.Fatal("valid")
	}
	r.ApprovalScopeDigest = "new"
	if r.ValidCloseAuthorization() {
		t.Fatal("stale")
	}
	r.ApprovalScopeDigest = "d"
	r.CloseAuthorization.Voided = true
	if r.ValidCloseAuthorization() {
		t.Fatal("voided")
	}
}

func TestCloseAuthorizationRequiresExactDeliveryBinding(t *testing.T) {
	r := Run{Repository: "repo", BaseCommit: "base", TargetRef: "refs/heads/main", ApprovalScopeDigest: "scope", CloseAuthorization: &CloseAuthorization{User: "user", Repository: "repo", BaseCommit: "base", TargetRef: "refs/heads/main", ApprovalScopeDigest: "scope"}}
	if !r.ValidCloseAuthorization() {
		t.Fatal("valid delivery refused")
	}
	for _, field := range []string{"repository", "base", "target", "missing"} {
		changed := r
		switch field {
		case "repository":
			changed.Repository = "other"
		case "base":
			changed.BaseCommit = "other"
		case "target":
			changed.TargetRef = "refs/heads/other"
		case "missing":
			changed.Repository = ""
		}
		if changed.ValidCloseAuthorization() {
			t.Fatalf("%s change retained consent", field)
		}
	}
	if (Run{ApprovalScopeDigest: "scope", CloseAuthorization: &CloseAuthorization{User: "user", ApprovalScopeDigest: "scope"}}).ValidCloseAuthorization() {
		t.Fatal("unbound legacy authority accepted")
	}
}
