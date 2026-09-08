package plan_test

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/plan"
)

func assignedPlan(t *testing.T) plan.ExecutionPlan {
	t.Helper()
	p := readyPlan(t)
	assigned := plan.AssignHarnesses(plan.AssignRequest{
		Team:       p.Team,
		Tasks:      p.Tasks,
		Assessment: p.Assessment,
		Candidates: []plan.HarnessCandidate{
			governedCandidate("codex", "openai"),
			governedCandidate("claude", "anthropic"),
		},
		Now: planTime,
	})
	p.Assignments = assigned
	return p
}

func dangerousPlan(t *testing.T) plan.ExecutionPlan {
	t.Helper()
	goal := confirmedGoal()
	goal.SuccessCriteria = []string{"the data is removed"}
	tasks := []plan.Task{
		{
			ID: "inspect", Title: "inspect the target records", Weight: 1,
			Criteria: []string{"the data is removed"},
		},
		{
			ID: "remove", Title: "delete the customer records", Mutating: true,
			Weight: 2, DependsOn: []string{"inspect"}, Checkpoint: true,
			Criteria: []string{"the data is removed"},
		},
	}
	p := buildPlan(t, plan.BuildRequest{
		Goal:       goal,
		ProjectID:  testProject,
		Assessment: dangerousAssessment(),
		Tasks:      tasks,
		Candidates: governedCandidates(),
	})
	assigned := plan.AssignHarnesses(plan.AssignRequest{
		Team:       p.Team,
		Tasks:      p.Tasks,
		Assessment: p.Assessment,
		Candidates: []plan.HarnessCandidate{
			governedCandidate("codex", "openai"),
			governedCandidate("claude", "anthropic"),
		},
		Now: planTime,
	})
	p.Assignments = assigned
	return p
}

// A person reviewing a plan must see at a glance what work is planned and which
// roles will carry it out, so that scope and responsibility are transparent.
func TestSummaryNamesTasksAndRoles(t *testing.T) {
	p := readyPlan(t)
	summary := plan.Summarise(p)

	for _, task := range p.Tasks {
		if !strings.Contains(summary, task.ID) {
			t.Fatalf("summary does not name task ID %q: %s", task.ID, summary)
		}
		if !strings.Contains(summary, task.Title) {
			t.Fatalf("summary does not name task title %q: %s", task.Title, summary)
		}
	}

	for _, member := range p.Team.Members {
		if !strings.Contains(summary, string(member.Role)) {
			t.Fatalf("summary does not name role %q: %s", member.Role, summary)
		}
		if !strings.Contains(summary, member.Reason) {
			t.Fatalf("summary does not give reason for role %q: %s", member.Role, summary)
		}
	}
}

// Requesting an explanation for a task not present in the plan must fail
// explicitly and identify the unknown identifier, rather than inventing an
// explanation for a non-existent task.
func TestUnknownTaskIDReturnsErrorNamingIt(t *testing.T) {
	p := readyPlan(t)
	missingID := "nonexistent-task-id"
	_, err := plan.ExplainDecision(p, missingID)
	if err == nil {
		t.Fatal("expected error for unknown task, got nil")
	}
	if !strings.Contains(err.Error(), missingID) {
		t.Fatalf("error %q does not name the missing task %q", err.Error(), missingID)
	}
}

// Routing decisions must be transparent to operators: the explanation must state
// which harness was selected and trace back to the evidence that justified it.
func TestExplainDecisionMentionsHarnessAndReason(t *testing.T) {
	p := assignedPlan(t)
	explanation, err := plan.ExplainDecision(p, "implement")
	if err != nil {
		t.Fatalf("explain decision: %v", err)
	}

	if !strings.Contains(explanation, "codex") {
		t.Fatalf("explanation does not mention assigned harness: %s", explanation)
	}
	if !strings.Contains(explanation, "MARSHAL can govern codex") {
		t.Fatalf("explanation does not mention reason for choosing harness: %s", explanation)
	}
}

// When a plan is prevented from executing, the summary must clearly surface
// what is blocking it so operators know what needs resolution before work starts.
func TestSummaryReportsBlockersWhenPlanIsBlocked(t *testing.T) {
	// A plan with an uncovered criterion is blocked.
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
		t.Fatalf("expected plan state BLOCKED, got %s", built.State)
	}

	summary := plan.Summarise(built)
	if !strings.Contains(summary, "Blocked by:") {
		t.Fatalf("summary does not report blockers: %s", summary)
	}
	if !strings.Contains(summary, "existing tests pass") {
		t.Fatalf("summary does not name the specific blocker: %s", summary)
	}
}

// A plan represents future intent rather than historical execution; asserting
// that tests have passed or work was completed before execution would mislead operators.
func TestOutputDescribesIntentWithoutClaimingWorkHasRun(t *testing.T) {
	p := assignedPlan(t)
	summary := plan.Summarise(p)

	forbiddenPhrases := []string{
		"tests passed",
		"has passed",
		"have passed",
		"was verified",
		"has been verified",
		"work completed",
		"has run",
		"already verified",
	}

	for _, phrase := range forbiddenPhrases {
		if strings.Contains(strings.ToLower(summary), phrase) {
			t.Fatalf("summary claims past execution with phrase %q: %s", phrase, summary)
		}
	}

	for _, task := range p.Tasks {
		explanation, err := plan.ExplainDecision(p, task.ID)
		if err != nil {
			t.Fatalf("explain decision for %s: %v", task.ID, err)
		}
		for _, phrase := range forbiddenPhrases {
			if strings.Contains(strings.ToLower(explanation), phrase) {
				t.Fatalf("explanation for %s claims past execution with phrase %q: %s",
					task.ID, phrase, explanation)
			}
		}
	}
}

// The critical path measures dependency depth in units of tasks; presenting it
// as a duration would invent precision that MARSHAL cannot honestly measure.
func TestSummaryReportsCriticalPathAsTaskCount(t *testing.T) {
	p := readyPlan(t)
	summary := plan.Summarise(p)

	if !strings.Contains(summary, "2 tasks long") {
		t.Fatalf("summary does not report critical path length as a task count: %s", summary)
	}

	for _, unit := range []string{"minutes", "hours", "seconds", "ms", "duration"} {
		if strings.Contains(strings.ToLower(summary), unit) {
			t.Fatalf("summary presents critical path as time duration with %q: %s", unit, summary)
		}
	}
}

// Checkpoints and approval gates define safety boundaries; surfacing them in
// the summary ensures human oversight points are visible before execution begins.
func TestSummaryReportsCheckpointsAndApprovals(t *testing.T) {
	p := dangerousPlan(t)
	summary := plan.Summarise(p)

	if !strings.Contains(summary, "Checkpoints:") {
		t.Fatalf("summary does not contain checkpoints section: %s", summary)
	}
	if !strings.Contains(summary, "After remove:") {
		t.Fatalf("summary does not name checkpoint location: %s", summary)
	}
	if !strings.Contains(summary, "Approvals:") {
		t.Fatalf("summary does not contain approvals section: %s", summary)
	}
	if !strings.Contains(summary, "hard approval") {
		t.Fatalf("summary does not identify hard approval requirements: %s", summary)
	}
}

// Explaining a task requires stating its permissions and boundaries so that
// least-privilege enforcement and security constraints are inspectable.
func TestExplainDecisionReportsPolicyAllowedAndDeniedScope(t *testing.T) {
	p := assignedPlan(t)
	explanation, err := plan.ExplainDecision(p, "implement")
	if err != nil {
		t.Fatalf("explain decision: %v", err)
	}

	if !strings.Contains(explanation, "Allowed scope: internal/api") {
		t.Fatalf("explanation does not report allowed scope: %s", explanation)
	}
	if !strings.Contains(explanation, "read_project") || !strings.Contains(explanation, "write_project") {
		t.Fatalf("explanation does not report allowed capabilities: %s", explanation)
	}
	if !strings.Contains(explanation, "credentials and secret material") {
		t.Fatalf("explanation does not report denied scope: %s", explanation)
	}
}

// Verification obligations explain how task completion will be confirmed;
// describing what will check the task stops unverified changes reaching production.
func TestExplainDecisionReportsVerificationObligations(t *testing.T) {
	p := assignedPlan(t)
	explanation, err := plan.ExplainDecision(p, "implement")
	if err != nil {
		t.Fatalf("explain decision: %v", err)
	}

	if !strings.Contains(explanation, "Verification:") {
		t.Fatalf("explanation does not contain verification section: %s", explanation)
	}
	if !strings.Contains(explanation, "responses are cached") {
		t.Fatalf("explanation does not mention criterion: %s", explanation)
	}
	if !strings.Contains(explanation, "will inspect the change against the criterion") {
		t.Fatalf("explanation does not state future verification method: %s", explanation)
	}
}

// When a task has an alternative route, the operator must know what fallback
// is configured and what must be re-established if a failover occurs.
func TestExplainDecisionReportsFallbackAndRevalidationRequirements(t *testing.T) {
	p := assignedPlan(t)
	explanation, err := plan.ExplainDecision(p, "implement")
	if err != nil {
		t.Fatalf("explain decision: %v", err)
	}

	if !strings.Contains(explanation, "Fallback harness: claude") {
		t.Fatalf("explanation does not report fallback harness: %s", explanation)
	}
	if !strings.Contains(explanation, "capability support on the replacement harness") {
		t.Fatalf("explanation does not state what must be revalidated: %s", explanation)
	}
}

// When decisions or settings were not made, reporting them as unrecorded
// preserves truthfulness rather than presenting plausible-sounding guesses.
func TestUnrecordedFieldsAreReportedHonestly(t *testing.T) {
	// A plan built with only one candidate provider has no fallback at all.
	soleProviderPlan := buildPlan(t, plan.BuildRequest{
		Goal:       confirmedGoal(),
		ProjectID:  testProject,
		Assessment: routineAssessment(),
		Tasks:      coveringTasks(),
		Candidates: []goalintake.Candidate{
			{
				Provider:   "sole",
				Model:      "m",
				Governance: constitution.GovernanceVerified,
				Capacity:   goalintake.UnknownCapacity("sole", true),
			},
		},
	})

	explanation, err := plan.ExplainDecision(soleProviderPlan, "implement")
	if err != nil {
		t.Fatalf("explain decision: %v", err)
	}

	if !strings.Contains(explanation, "Assigned harness: not recorded") {
		t.Fatalf("explanation did not report missing harness as not recorded: %s", explanation)
	}
	if !strings.Contains(explanation, "No fallback is recorded") {
		t.Fatalf("explanation did not report absent fallback: %s", explanation)
	}
	if !strings.Contains(explanation, "This task requires no approvals.") {
		t.Fatalf("explanation did not report absence of approvals: %s", explanation)
	}
}

// The remaining two "not recorded" paths are covered here.
//
// Three separate branches can report a missing harness: one where an
// assignment exists, one where only a route does, and one where neither does.
// A test reaching only the middle branch leaves the other two free to invent a
// value, which is the specific failure this rule exists to prevent — so each
// is exercised directly.
func TestEveryAbsentAssignmentPathReportsHonestly(t *testing.T) {
	// Neither an assignment nor a route: the plan knows nothing about how this
	// task would run, and must say exactly that.
	bare := readyPlan(t)
	bare.Assignments = plan.AssignmentPlan{}
	bare.Routes = nil

	explanation, err := plan.ExplainDecision(bare, "implement")
	if err != nil {
		t.Fatalf("explain decision: %v", err)
	}
	for _, expected := range []string{
		"Assigned harness: not recorded",
		"Assigned model: not recorded",
		"Assignment reason: not recorded",
	} {
		if !strings.Contains(explanation, expected) {
			t.Fatalf("a plan with no assignment and no route did not report %q: %s",
				expected, explanation)
		}
	}

	// An assignment exists but names no model: the harness is reported and the
	// model is not invented from it.
	partial := assignedPlan(t)
	for i := range partial.Assignments.Assignments {
		partial.Assignments.Assignments[i].Model = ""
	}
	partialExplanation, err := plan.ExplainDecision(partial, "implement")
	if err != nil {
		t.Fatalf("explain decision: %v", err)
	}
	if !strings.Contains(partialExplanation, "Assigned model: not recorded") {
		t.Fatalf("an assignment with no model did not report it as unrecorded: %s",
			partialExplanation)
	}
	// The harness is still named, so an absent model does not erase what is known.
	if strings.Contains(partialExplanation, "Assigned harness: not recorded") {
		t.Fatalf("a known harness was reported as unrecorded: %s", partialExplanation)
	}
}

// An assignment that names no harness is the last of the three paths.
//
// It is the easiest one to fill in wrongly: an Assignment record exists, so it
// looks like the harness must be known, and a reader would trust a name that
// appeared here. It must still say the harness is not recorded.
func TestAssignmentWithoutAHarnessNameReportsHonestly(t *testing.T) {
	nameless := assignedPlan(t)
	for i := range nameless.Assignments.Assignments {
		nameless.Assignments.Assignments[i].Harness = ""
		nameless.Assignments.Assignments[i].HarnessVersion = ""
		nameless.Assignments.Assignments[i].Provider = ""
	}

	explanation, err := plan.ExplainDecision(nameless, "implement")
	if err != nil {
		t.Fatalf("explain decision: %v", err)
	}
	if !strings.Contains(explanation, "Assigned harness: not recorded") {
		t.Fatalf("an assignment with no harness name did not report it as unrecorded: %s",
			explanation)
	}
}
