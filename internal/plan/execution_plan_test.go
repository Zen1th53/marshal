package plan_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

const testProject = projectid.ID("PROJECT-0123456789abcdef0123456789abcdef")

var planTime = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func confirmedGoal() model.GoalContract {
	return model.GoalContract{
		ID:              "GOAL-1",
		SessionID:       "SESSION-1",
		ProjectID:       string(testProject),
		Revision:        1,
		OriginalRequest: "Add caching to the API. Do not modify the database schema.",
		RequestDigest:   "sha256:abc",
		DesiredOutcome:  "API responses are cached",
		Risk:            model.R1,
		AuthoritySource: "operator",
		Confirmation:    model.ConfirmationApproved,
		SuccessCriteria: []string{"responses are cached", "existing tests pass"},
		Constraints: []model.Constraint{
			{ID: "C1", Text: "Do not modify the database schema", Source: "user", IsHard: true},
		},
	}
}

func routineAssessment() goalintake.Assessment {
	return goalintake.AssessRequest("add caching to the api",
		goalintake.RequestContext{Recoverable: true, ScopeKnown: true})
}

func dangerousAssessment() goalintake.Assessment {
	return goalintake.AssessRequest("delete the production customer database and deploy",
		goalintake.RequestContext{Recoverable: true, ScopeKnown: true})
}

func governedCandidates() []goalintake.Candidate {
	return []goalintake.Candidate{
		{Provider: "codex", Model: "m", Governance: constitution.GovernanceVerified,
			Capacity: goalintake.UnknownCapacity("codex", true)},
		{Provider: "claude", Model: "m", Governance: constitution.GovernanceVerified,
			Capacity: goalintake.UnknownCapacity("claude", true)},
	}
}

func coveringTasks() []plan.Task {
	return []plan.Task{
		{ID: "implement", Title: "add the cache", Mutating: true, Weight: 3,
			Paths: []string{"internal/api"}, Criteria: []string{"responses are cached"}},
		{ID: "test", Title: "run the tests", Weight: 1, DependsOn: []string{"implement"},
			Criteria: []string{"existing tests pass"}},
	}
}

func buildPlan(t *testing.T, request plan.BuildRequest) plan.ExecutionPlan {
	t.Helper()
	if request.Now.IsZero() {
		request.Now = planTime
	}
	if request.Version.IsZero() {
		request.Version = constitution.Current
	}
	built, err := plan.Build(request)
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	return built
}

func readyPlan(t *testing.T) plan.ExecutionPlan {
	t.Helper()
	return buildPlan(t, plan.BuildRequest{
		Goal: confirmedGoal(), ProjectID: testProject,
		Assessment: routineAssessment(), Tasks: coveringTasks(),
		Candidates: governedCandidates(),
	})
}

func TestCompletePlanReachesReady(t *testing.T) {
	built := readyPlan(t)
	if built.State != plan.StateReady {
		t.Fatalf("a complete plan is %s: %v", built.State, built.BlockedBy)
	}
	if built.Version != 1 {
		t.Fatalf("a new plan is version %d", built.Version)
	}
	if len(built.Graph.Order) != 2 {
		t.Fatalf("the graph covers %v", built.Graph.Order)
	}
	if built.Assessment.Complexity != routineAssessment().Complexity {
		t.Fatal("the plan recomputed the assessment rather than inheriting it")
	}
}

// READY is not permission to execute. A plan being complete and a person
// agreeing to it are different facts.
func TestReadyIsNotExecutable(t *testing.T) {
	built := readyPlan(t)
	if built.State.Executable() {
		t.Fatal("a READY plan was executable without approval")
	}
	approved, err := built.Approve(planTime)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !approved.State.Executable() {
		t.Fatal("an approved plan was not executable")
	}

	for _, state := range []plan.State{
		plan.StateDraft, plan.StateNeedsInput, plan.StateStale,
		plan.StateBlocked, plan.StateCancelled,
	} {
		if state.Executable() {
			t.Fatalf("state %s was executable", state)
		}
	}
}

// A blocked plan cannot be approved, because approving it would approve
// whatever the blockers were hiding.
func TestBlockedPlanCannotBeApproved(t *testing.T) {
	// A criterion nothing covers blocks the plan.
	built := buildPlan(t, plan.BuildRequest{
		Goal: confirmedGoal(), ProjectID: testProject,
		Assessment: routineAssessment(),
		Tasks: []plan.Task{
			{ID: "implement", Title: "add the cache", Mutating: true, Weight: 1,
				Criteria: []string{"responses are cached"}},
		},
		Candidates: governedCandidates(),
	})
	if built.State != plan.StateBlocked {
		t.Fatalf("a plan leaving a criterion unchecked is %s", built.State)
	}
	if _, err := built.Approve(planTime); err == nil {
		t.Fatal("a blocked plan was approved")
	}
	joined := strings.Join(built.BlockedBy, " ")
	if !strings.Contains(joined, "existing tests pass") {
		t.Fatalf("the blocker does not name the unchecked criterion: %s", joined)
	}
}

// An unconfirmed goal produces a blocked plan carrying the reason, rather than
// an error the caller might ignore.
func TestUnconfirmedGoalBlocksPlanning(t *testing.T) {
	goal := confirmedGoal()
	goal.Confirmation = model.ConfirmationPending

	built := buildPlan(t, plan.BuildRequest{
		Goal: goal, ProjectID: testProject, Assessment: routineAssessment(),
		Tasks: coveringTasks(), Candidates: governedCandidates(),
	})
	if built.State != plan.StateBlocked {
		t.Fatalf("planning from an unconfirmed goal produced %s", built.State)
	}
	if len(built.BlockedBy) == 0 {
		t.Fatal("the block carries no reason")
	}
}

// Planning against the wrong project is refused.
func TestProjectMismatchBlocksPlanning(t *testing.T) {
	built := buildPlan(t, plan.BuildRequest{
		Goal:       confirmedGoal(),
		ProjectID:  projectid.ID("PROJECT-fedcba9876543210fedcba9876543210"),
		Assessment: routineAssessment(), Tasks: coveringTasks(),
		Candidates: governedCandidates(),
	})
	if built.State != plan.StateBlocked {
		t.Fatalf("a project mismatch produced %s", built.State)
	}
}

// A revised goal stales the plan, and the plan says what changed.
func TestGoalRevisionStalesThePlan(t *testing.T) {
	built := readyPlan(t)

	revised := confirmedGoal()
	revised.Revision = 2
	stale := built.Restale(revised, planTime)

	if stale.State != plan.StateStale {
		t.Fatalf("a revised goal left the plan %s", stale.State)
	}
	if len(stale.BlockedBy) == 0 {
		t.Fatal("a stale plan does not say what changed")
	}
	if !strings.Contains(strings.Join(stale.BlockedBy, " "), "revised") {
		t.Fatalf("the reason does not mention the revision: %v", stale.BlockedBy)
	}

	// An unchanged goal leaves the plan alone.
	if built.Restale(confirmedGoal(), planTime).State != plan.StateReady {
		t.Fatal("an unchanged goal staled the plan")
	}
}

// A changed constraint stales the plan; a changed preference does not.
func TestConstraintChangeStalesButPreferenceDoesNot(t *testing.T) {
	built := readyPlan(t)

	withNewConstraint := confirmedGoal()
	withNewConstraint.Constraints = append(withNewConstraint.Constraints,
		model.Constraint{ID: "C2", Text: "Do not touch the auth module", IsHard: true})
	if built.Restale(withNewConstraint, planTime).State != plan.StateStale {
		t.Fatal("adding a hard constraint did not stale the plan")
	}

	withPreference := confirmedGoal()
	withPreference.Constraints = append(withPreference.Constraints,
		model.Constraint{ID: "P1", Text: "prefer table-driven tests", IsHard: false})
	if built.Restale(withPreference, planTime).State == plan.StateStale {
		t.Fatal("adding a soft preference staled the plan; the signal would stop being read")
	}
}

// Revision is CAS-guarded: a stale writer is told rather than winning.
func TestRevisionRefusesAStaleWrite(t *testing.T) {
	built := readyPlan(t)

	revised, err := built.Revise(built.Version, "the user asked for a different approach", planTime)
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	if revised.Version != built.Version+1 {
		t.Fatalf("revision produced version %d", revised.Version)
	}
	if revised.Supersedes != built.Version {
		t.Fatal("the revision does not record what it replaced")
	}
	// A revised plan returns to draft: the user agreed to the previous
	// version, not this one.
	if revised.State != plan.StateDraft {
		t.Fatalf("a revised plan is %s rather than returning for review", revised.State)
	}

	// A second writer holding the old version is refused.
	if _, err := revised.Revise(built.Version, "a conflicting edit", planTime); err == nil {
		t.Fatal("a stale write overwrote a newer plan")
	}
	// A revision must say why.
	if _, err := built.Revise(built.Version, "  ", planTime); err == nil {
		t.Fatal("a revision was accepted with no reason")
	}
}

// Approving a plan does not survive a revision.
func TestApprovalDoesNotSurviveRevision(t *testing.T) {
	approved, err := readyPlan(t).Approve(planTime)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	revised, err := approved.Revise(approved.Version, "changed scope", planTime)
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	if revised.State.Executable() {
		t.Fatal("a revised plan carried its predecessor's approval")
	}
}

// A dangerous goal produces hard approval requirements, and they are
// requirements rather than approvals.
func TestDangerousWorkPlansHardApprovals(t *testing.T) {
	goal := confirmedGoal()
	goal.SuccessCriteria = []string{"the data is removed"}

	built := buildPlan(t, plan.BuildRequest{
		Goal: goal, ProjectID: testProject, Assessment: dangerousAssessment(),
		Tasks: []plan.Task{
			{ID: "remove", Title: "remove the data", Mutating: true, Weight: 1,
				Criteria: []string{"the data is removed"}},
		},
		Candidates: governedCandidates(),
	})

	if len(built.Approvals) == 0 {
		t.Fatal("destructive work planned no approval")
	}
	hard := false
	for _, approval := range built.Approvals {
		if approval.Hard {
			hard = true
		}
		if strings.TrimSpace(approval.Reason) == "" {
			t.Fatal("an approval requirement gives no reason")
		}
	}
	if !hard {
		t.Fatal("destructive work planned no hard approval")
	}
	// Planning an approval is not obtaining one: the plan is at most ready.
	if built.State.Executable() {
		t.Fatal("planning an approval made the plan executable")
	}
}

// Hard-to-undo work gets a restore point before the first change.
func TestIrreversibleWorkGetsACheckpointBeforeTheFirstChange(t *testing.T) {
	goal := confirmedGoal()
	goal.SuccessCriteria = []string{"the data is removed"}

	built := buildPlan(t, plan.BuildRequest{
		Goal: goal, ProjectID: testProject, Assessment: dangerousAssessment(),
		Tasks: []plan.Task{
			{ID: "inspect", Title: "look first", Weight: 1},
			{ID: "remove", Title: "remove the data", Mutating: true, Weight: 1,
				DependsOn: []string{"inspect"}, Criteria: []string{"the data is removed"}},
		},
		Candidates: governedCandidates(),
	})

	if len(built.Checkpoints) == 0 {
		t.Fatal("hard-to-undo work planned no restore point")
	}
	if built.Checkpoints[0].AfterTask != "remove" {
		t.Fatalf("the checkpoint is at %q, want the first mutating task", built.Checkpoints[0].AfterTask)
	}
}

// Routing reuses the Process 03 selector, so governance outranks capacity here
// exactly as it does at intake.
func TestRoutingPrefersGovernedProviders(t *testing.T) {
	built := buildPlan(t, plan.BuildRequest{
		Goal: confirmedGoal(), ProjectID: testProject,
		Assessment: routineAssessment(), Tasks: coveringTasks(),
		Candidates: []goalintake.Candidate{
			{Provider: "ungoverned", Model: "m", Governance: constitution.GovernanceUnverified,
				Capacity: goalintake.UnknownCapacity("ungoverned", true)},
			{Provider: "governed", Model: "m", Governance: constitution.GovernanceVerified,
				Capacity: goalintake.UnknownCapacity("governed", true)},
		},
	})
	for id, route := range built.Routes {
		if route.Provider != "governed" {
			t.Fatalf("task %s routed to %q rather than the governed provider", id, route.Provider)
		}
	}
}

// With no usable provider a plan is blocked rather than routed to something
// ungovernable, and the cause is stated once rather than per task.
func TestNoUsableProviderBlocksThePlan(t *testing.T) {
	built := buildPlan(t, plan.BuildRequest{
		Goal: confirmedGoal(), ProjectID: testProject,
		Assessment: routineAssessment(), Tasks: coveringTasks(),
		Candidates: []goalintake.Candidate{
			{Provider: "rogue", Model: "m", Governance: constitution.GovernanceUnavailable,
				Capacity: goalintake.UnknownCapacity("rogue", true)},
		},
	})
	if built.State != plan.StateBlocked {
		t.Fatalf("a plan with no usable provider is %s", built.State)
	}
	if len(built.Unknowns) != 1 {
		t.Fatalf("the cause was repeated per task rather than stated once: %v", built.Unknowns)
	}
}

// A sole provider with no alternative is recorded as an unknown, so the plan
// does not look more resilient than it is.
func TestMissingFallbackIsRecordedAsAnUnknown(t *testing.T) {
	built := buildPlan(t, plan.BuildRequest{
		Goal: confirmedGoal(), ProjectID: testProject,
		Assessment: routineAssessment(), Tasks: coveringTasks(),
		Candidates: []goalintake.Candidate{
			{Provider: "only", Model: "m", Governance: constitution.GovernanceVerified,
				Capacity: goalintake.UnknownCapacity("only", true)},
		},
	})
	if len(built.Unknowns) == 0 {
		t.Fatal("a plan with no fallback recorded no unknown")
	}
	if !strings.Contains(strings.Join(built.Unknowns, " "), "no alternative") {
		t.Fatalf("the unknown does not name the missing fallback: %v", built.Unknowns)
	}
}

// The budget bounds work rather than time or money, because those are figures
// MARSHAL cannot measure honestly.
func TestBudgetBoundsWorkAndPausesWhenWatchingIsWarranted(t *testing.T) {
	routine := readyPlan(t)
	if routine.Budget.MaxTasks != len(routine.Tasks) {
		t.Fatalf("the budget bounds %d tasks for %d", routine.Budget.MaxTasks, len(routine.Tasks))
	}
	if routine.Budget.RequiresCheckIn {
		t.Fatal("routine work was made to pause partway")
	}

	goal := confirmedGoal()
	goal.SuccessCriteria = []string{"the data is removed"}
	dangerous := buildPlan(t, plan.BuildRequest{
		Goal: goal, ProjectID: testProject, Assessment: dangerousAssessment(),
		Tasks: []plan.Task{
			{ID: "remove", Title: "remove", Mutating: true, Weight: 1,
				Criteria: []string{"the data is removed"}},
		},
		Candidates: governedCandidates(),
	})
	if !dangerous.Budget.RequiresCheckIn {
		t.Fatal("work needing watching does not pause")
	}
}
