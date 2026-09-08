package plan_test

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func approvedPlan(t *testing.T) plan.ExecutionPlan {
	t.Helper()
	approved, err := readyPlan(t).Approve(planTime)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	return approved
}

func refusalReasons(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("the handoff gate let the work through")
	}
	return err.Error()
}

func TestApprovedPlanHandsOffCleanly(t *testing.T) {
	handoff, err := plan.PrepareHandoff(approvedPlan(t), confirmedGoal(), testProject, planTime)
	if err != nil {
		t.Fatalf("an approved, current plan was refused: %v", err)
	}
	if err := handoff.Validate(); err != nil {
		t.Fatalf("the gate produced a handoff its own receiver rejects: %v", err)
	}

	// The original request travels, so execution is checked against what was
	// asked rather than the plan's paraphrase of it.
	if handoff.OriginalRequest != confirmedGoal().OriginalRequest {
		t.Fatal("the handoff does not carry the original request")
	}
	// Hard constraints are restated rather than referenced.
	if len(handoff.HardConstraints) == 0 {
		t.Fatal("the handoff carries no hard constraints")
	}
	if !strings.Contains(strings.Join(handoff.HardConstraints, " "), "database schema") {
		t.Fatalf("the goal's hard constraint was lost: %v", handoff.HardConstraints)
	}
	if handoff.Goal.Revision != confirmedGoal().Revision {
		t.Fatal("the handoff does not pin the goal revision")
	}
	if handoff.IssuedAt.IsZero() {
		t.Fatal("the handoff is not timestamped")
	}
}

// READY is not APPROVED. The gate is the place that distinction has to hold.
func TestUnapprovedPlanIsRefusedAtTheGate(t *testing.T) {
	_, err := plan.PrepareHandoff(readyPlan(t), confirmedGoal(), testProject, planTime)
	if !strings.Contains(refusalReasons(t, err), "approved") {
		t.Fatalf("the refusal does not say approval is missing: %v", err)
	}
}

// The plan was checked when it was built; the gate asks whether it is current
// now. A goal revised after approval must not run under the old plan.
func TestGoalRevisedAfterApprovalIsRefused(t *testing.T) {
	revised := confirmedGoal()
	revised.Revision = 2

	_, err := plan.PrepareHandoff(approvedPlan(t), revised, testProject, planTime)
	refusalReasons(t, err)
}

// An approval withdrawn between planning and execution takes effect.
func TestWithdrawnConfirmationIsRefused(t *testing.T) {
	withdrawn := confirmedGoal()
	withdrawn.Confirmation = model.ConfirmationPending

	_, err := plan.PrepareHandoff(approvedPlan(t), withdrawn, testProject, planTime)
	if !strings.Contains(refusalReasons(t, err), "confirmed") {
		t.Fatalf("the refusal does not name the withdrawn confirmation: %v", err)
	}
}

func TestProjectMismatchIsRefusedAtTheGate(t *testing.T) {
	other := projectid.ID("PROJECT-fedcba9876543210fedcba9876543210")
	_, err := plan.PrepareHandoff(approvedPlan(t), confirmedGoal(), other, planTime)
	if !strings.Contains(refusalReasons(t, err), "different project") {
		t.Fatalf("the refusal does not name the project mismatch: %v", err)
	}
}

// A plan whose tasks changed after approval is refused: the approval was for
// the tasks that were shown.
func TestTasksChangedAfterApprovalAreRefused(t *testing.T) {
	// A task is edited in place rather than added, so the task count, the
	// ordering and the routes all still line up. Only the digest recorded when
	// the plan was approved can notice, which is exactly what it is for: a
	// read-only step becoming a mutating one over a wider blast radius is the
	// change most worth catching and the least visible.
	tampered := approvedPlan(t)
	tampered.Tasks = append([]plan.Task(nil), tampered.Tasks...)
	for i, task := range tampered.Tasks {
		if task.ID == "test" {
			tampered.Tasks[i].Mutating = true
			tampered.Tasks[i].Paths = []string{"internal"}
		}
	}

	_, err := plan.PrepareHandoff(tampered, confirmedGoal(), testProject, planTime)
	if !strings.Contains(refusalReasons(t, err), "changed since it was built") {
		t.Fatalf("an edited task passed the gate on the approval given to the original: %v", err)
	}
}

// A route to a provider MARSHAL cannot govern is not a route.
func TestUngovernableRouteIsRefused(t *testing.T) {
	tampered := approvedPlan(t)
	routes := map[string]plan.Route{}
	for id, route := range tampered.Routes {
		route.Governance = constitution.GovernanceUnavailable
		routes[id] = route
	}
	tampered.Routes = routes

	_, err := plan.PrepareHandoff(tampered, confirmedGoal(), testProject, planTime)
	if !strings.Contains(refusalReasons(t, err), "governed") {
		t.Fatalf("the refusal does not name the ungovernable provider: %v", err)
	}
}

// A task with no provider is refused rather than started and abandoned.
func TestUnroutedTaskIsRefused(t *testing.T) {
	tampered := approvedPlan(t)
	routes := map[string]plan.Route{}
	for id, route := range tampered.Routes {
		if id == "test" {
			continue
		}
		routes[id] = route
	}
	tampered.Routes = routes

	_, err := plan.PrepareHandoff(tampered, confirmedGoal(), testProject, planTime)
	if !strings.Contains(refusalReasons(t, err), "test") {
		t.Fatalf("the refusal does not name the unrouted task: %v", err)
	}
}

// Work whose success nobody can check does not start.
func TestUnverifiableCriterionIsRefused(t *testing.T) {
	tampered := approvedPlan(t)
	tampered.Verification.Uncovered = []string{"latency drops below 50ms"}

	_, err := plan.PrepareHandoff(tampered, confirmedGoal(), testProject, planTime)
	if !strings.Contains(refusalReasons(t, err), "check") {
		t.Fatalf("the refusal does not name the verification gap: %v", err)
	}
}

// Several faults are reported together, so fixing one does not just reveal the
// next on a second attempt.
func TestAllFailingChecksAreReportedTogether(t *testing.T) {
	stale := confirmedGoal()
	stale.Revision = 2
	stale.Confirmation = model.ConfirmationPending

	other := projectid.ID("PROJECT-fedcba9876543210fedcba9876543210")
	_, err := plan.PrepareHandoff(readyPlan(t), stale, other, planTime)

	refusal := &plan.HandoffRefusal{}
	if !asRefusal(err, refusal) {
		t.Fatalf("the gate returned %T rather than a structured refusal", err)
	}
	if len(refusal.Reasons) < 3 {
		t.Fatalf("only %d of several faults were reported: %v", len(refusal.Reasons), refusal.Reasons)
	}
}

// The receiver validates rather than trusting the sender, so a defect on the
// planning side cannot become an execution missing its constraints.
func TestReceiverRejectsAnIncompleteHandoff(t *testing.T) {
	handoff, err := plan.PrepareHandoff(approvedPlan(t), confirmedGoal(), testProject, planTime)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	cases := map[string]func(*plan.Handoff){
		"no original request": func(h *plan.Handoff) { h.OriginalRequest = "" },
		"no goal":             func(h *plan.Handoff) { h.Goal.GoalID = "" },
		"no goal revision":    func(h *plan.Handoff) { h.Goal.Revision = 0 },
		"no project":          func(h *plan.Handoff) { h.ProjectID = "" },
		"no plan version":     func(h *plan.Handoff) { h.PlanVersion = 0 },
		"no constitution":     func(h *plan.Handoff) { h.ConstitutionVersion = constitution.Version{} },
		"no work":             func(h *plan.Handoff) { h.Tasks = nil },
		"no team":             func(h *plan.Handoff) { h.Team = plan.Team{} },
		"unverifiable":        func(h *plan.Handoff) { h.Verification.Uncovered = []string{"x"} },
		"no route":            func(h *plan.Handoff) { h.Routes = nil },
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			broken := handoff
			damage(&broken)
			if err := broken.Validate(); err == nil {
				t.Fatalf("a handoff with %s was accepted", name)
			}
		})
	}
}

// The handoff carries canonical state only: no provider conversation, so
// Process 05 can start on a provider that has never seen this work.
func TestHandoffCarriesNoProviderConversation(t *testing.T) {
	handoff, err := plan.PrepareHandoff(approvedPlan(t), confirmedGoal(), testProject, planTime)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	for id, route := range handoff.Routes {
		if route.Provider == "" {
			t.Fatalf("task %s has an empty provider", id)
		}
	}

	// Unknowns travel, so execution begins knowing what planning could not settle.
	single := buildPlan(t, plan.BuildRequest{
		Goal: confirmedGoal(), ProjectID: testProject,
		Assessment: routineAssessment(), Tasks: coveringTasks(),
		Candidates: []goalintake.Candidate{
			{Provider: "only", Model: "m", Governance: constitution.GovernanceVerified,
				Capacity: goalintake.UnknownCapacity("only", true)},
		},
	})
	approvedSingle, err := single.Approve(planTime)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	carried, err := plan.PrepareHandoff(approvedSingle, confirmedGoal(), testProject, planTime)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(carried.Unknowns) == 0 {
		t.Fatal("the plan's unknowns did not travel with the handoff")
	}
}

func asRefusal(err error, target *plan.HandoffRefusal) bool {
	refusal, ok := err.(*plan.HandoffRefusal)
	if !ok {
		return false
	}
	*target = *refusal
	return true
}
