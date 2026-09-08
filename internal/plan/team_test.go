package plan_test

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/plan"
)

func assess(request string) goalintake.Assessment {
	return goalintake.AssessRequest(request,
		goalintake.RequestContext{Recoverable: true, ScopeKnown: true})
}

// A small contained change gets a small team. Adding roles "to be safe" costs
// budget and attention, and makes the roles stop meaning anything.
func TestSmallChangeGetsASmallTeam(t *testing.T) {
	team := plan.AssembleTeam(
		assess("fix a typo in the readme"),
		[]plan.Task{{ID: "edit", Title: "fix the typo", Mutating: true, Weight: 1}},
		nil,
	)

	if !team.Has(plan.RoleDeveloper) {
		t.Fatal("nobody was assigned to do the work")
	}
	for _, role := range []plan.Role{plan.RoleArchitect, plan.RoleAppSec, plan.RoleOrchestrator} {
		if team.Has(role) {
			t.Fatalf("a typo fix pulled in %s", role)
		}
	}
	if len(team.Excluded) == 0 {
		t.Fatal("the roles left out are not recorded, so a small team looks like an oversight")
	}
	for _, member := range team.Excluded {
		if strings.TrimSpace(member.Reason) == "" {
			t.Fatalf("role %s was excluded with no reason", member.Role)
		}
	}
}

// Security-shaped work pulls in the security reviewer, and the reviewer is
// independent.
func TestSecuritySensitiveWorkAddsAppSec(t *testing.T) {
	team := plan.AssembleTeam(
		assess("rewrite the authentication flow and rotate the api credentials"),
		[]plan.Task{{ID: "auth", Title: "rewrite auth", Mutating: true, Weight: 3,
			Paths: []string{"internal/auth"}}},
		[]string{"login still works"},
	)

	if !team.Has(plan.RoleAppSec) {
		t.Fatalf("credential work did not pull in security review: %v", team.Roles())
	}
	for _, member := range team.Members {
		if member.Role == plan.RoleAppSec && !member.Independent {
			t.Fatal("the security reviewer was not marked independent")
		}
	}
}

// Every selected role states what called for it.
func TestEverySelectedRoleGivesAReason(t *testing.T) {
	team := plan.AssembleTeam(
		assess("migrate the whole service to a new database and delete the old one"),
		[]plan.Task{
			{ID: "a", Title: "a", Mutating: true, Weight: 2, Paths: []string{"internal/store"}},
			{ID: "b", Title: "b", Mutating: true, Weight: 2, Paths: []string{"internal/api"}},
		},
		[]string{"data is migrated"},
	)
	if len(team.Members) == 0 {
		t.Fatal("no team was assembled")
	}
	for _, member := range team.Members {
		if strings.TrimSpace(member.Reason) == "" {
			t.Fatalf("role %s was added with no stated reason", member.Role)
		}
	}
}

// Concurrent work gets a coordinator; sequential work does not, because
// coordination of a single chain is pure overhead.
func TestOrchestratorOnlyWhenWorkIsConcurrent(t *testing.T) {
	sequential := plan.AssembleTeam(assess("update the docs"), []plan.Task{
		{ID: "a", Title: "a", Weight: 1},
		{ID: "b", Title: "b", Weight: 1, DependsOn: []string{"a"}},
	}, nil)
	if sequential.Has(plan.RoleOrchestrator) {
		t.Fatal("a sequential plan was given a coordinator")
	}

	concurrent := plan.AssembleTeam(assess("update the docs"), []plan.Task{
		{ID: "a", Title: "a", Weight: 1},
		{ID: "b", Title: "b", Weight: 1},
	}, nil)
	if !concurrent.Has(plan.RoleOrchestrator) {
		t.Fatal("concurrent work was left uncoordinated")
	}
}

// Acceptance criteria call for a checker, and that checker is independent of
// whoever does the work.
func TestCriteriaCallForAnIndependentChecker(t *testing.T) {
	team := plan.AssembleTeam(
		assess("add caching to the api"),
		[]plan.Task{{ID: "impl", Title: "impl", Mutating: true, Weight: 1}},
		[]string{"responses are cached"},
	)
	if !team.Has(plan.RoleQA) {
		t.Fatal("a goal with acceptance criteria got nobody to check them")
	}
	verifier, ok := team.IndependentVerifier()
	if !ok {
		t.Fatal("no independent verifier was named")
	}
	if verifier == plan.RoleDeveloper {
		t.Fatal("the developer was named as its own independent verifier")
	}
}

// Verification obligations are commitments to check, never claims of having
// checked.
func TestVerificationCoversCriteriaAndNamesTheTasks(t *testing.T) {
	tasks := []plan.Task{
		{ID: "impl", Title: "impl", Mutating: true, Weight: 1,
			Criteria: []string{"responses are cached"}},
		{ID: "test", Title: "test", Weight: 1, DependsOn: []string{"impl"},
			Criteria: []string{"the tests pass"}},
	}
	criteria := []string{"responses are cached", "the tests pass"}
	team := plan.AssembleTeam(assess("add caching"), tasks, criteria)

	verification := plan.PlanVerification(criteria, tasks, assess("add caching"), team)
	if !verification.Complete() {
		t.Fatalf("a fully covered goal reported gaps: %v", verification.Uncovered)
	}
	if len(verification.Obligations) != 2 {
		t.Fatalf("got %d obligations for 2 criteria", len(verification.Obligations))
	}
	for _, obligation := range verification.Obligations {
		if strings.TrimSpace(obligation.Method) == "" {
			t.Fatalf("criterion %q has no method", obligation.Criterion)
		}
		if len(obligation.Tasks) == 0 {
			t.Fatalf("criterion %q names no task producing it", obligation.Criterion)
		}
	}
	// The method reflects the criterion rather than being one fixed string.
	if verification.Obligations[0].Method == verification.Obligations[1].Method {
		t.Fatalf("both criteria got the same generic method: %q", verification.Obligations[0].Method)
	}
}

// A criterion nothing produces is reported, not covered by an invented check.
func TestUncoveredCriterionIsReportedRatherThanInvented(t *testing.T) {
	tasks := []plan.Task{{ID: "impl", Title: "impl", Mutating: true, Weight: 1,
		Criteria: []string{"responses are cached"}}}
	criteria := []string{"responses are cached", "latency drops below 50ms"}
	team := plan.AssembleTeam(assess("add caching"), tasks, criteria)

	verification := plan.PlanVerification(criteria, tasks, assess("add caching"), team)
	if verification.Complete() {
		t.Fatal("a criterion nothing produces was reported as covered")
	}
	if len(verification.Uncovered) != 1 || verification.Uncovered[0] != "latency drops below 50ms" {
		t.Fatalf("uncovered criteria were %v", verification.Uncovered)
	}
	// The covered one still has its obligation: one gap does not void the rest.
	if len(verification.Obligations) != 1 {
		t.Fatalf("got %d obligations; a gap should not discard the covered criterion", len(verification.Obligations))
	}
}

// Hard-to-undo work makes its criteria critical, so a gap there cannot be
// waved through.
func TestHardToUndoWorkMarksCriteriaCritical(t *testing.T) {
	tasks := []plan.Task{{ID: "drop", Title: "drop", Mutating: true, Weight: 1,
		Criteria: []string{"the data is removed"}}}
	criteria := []string{"the data is removed"}

	dangerous := goalintake.AssessRequest("permanently delete the production database",
		goalintake.RequestContext{Recoverable: false, ScopeKnown: true})
	team := plan.AssembleTeam(dangerous, tasks, criteria)

	verification := plan.PlanVerification(criteria, tasks, dangerous, team)
	if len(verification.Obligations) != 1 {
		t.Fatalf("got %d obligations", len(verification.Obligations))
	}
	if !verification.Obligations[0].Critical {
		t.Fatal("a criterion for irreversible work was not marked critical")
	}
	if !verification.Obligations[0].Independent {
		t.Fatal("irreversible work would have been checked by whoever did it")
	}
}

// Team selection is deterministic, so the same plan shown twice reads the same.
func TestTeamSelectionIsDeterministic(t *testing.T) {
	assessment := assess("rewrite the auth module and migrate the database")
	tasks := []plan.Task{
		{ID: "a", Title: "a", Mutating: true, Weight: 2, Paths: []string{"internal/auth"}},
		{ID: "b", Title: "b", Mutating: true, Weight: 2, Paths: []string{"internal/store"}},
	}
	first := plan.AssembleTeam(assessment, tasks, []string{"login works"})
	for i := 0; i < 10; i++ {
		next := plan.AssembleTeam(assessment, tasks, []string{"login works"})
		if strings.Join(rolesOf(next), ",") != strings.Join(rolesOf(first), ",") {
			t.Fatal("repeated assembly produced different teams")
		}
	}
}

func rolesOf(team plan.Team) []string {
	var out []string
	for _, role := range team.Roles() {
		out = append(out, string(role))
	}
	return out
}
