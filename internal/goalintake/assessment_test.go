package goalintake_test

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/goalintake"
)

func safeContext() goalintake.RequestContext {
	return goalintake.RequestContext{Recoverable: true, ScopeKnown: true}
}

func assess(request string) goalintake.Assessment {
	return goalintake.AssessRequest(request, safeContext())
}

// The distinction the whole package exists for: a large safe change and a
// small dangerous one must not look alike.
func TestComplexityIsNotRisk(t *testing.T) {
	// Large, entirely reversible, touches nothing outside the project.
	complex := assess("refactor and rewrite the architecture of the reporting module")
	// Trivial in effort, and catastrophic.
	dangerous := assess("delete the production customer data")

	if !complex.Complexity.AtLeast(goalintake.LevelMed) {
		t.Fatalf("a rewrite was assessed as complexity %s", complex.Complexity)
	}
	if complex.RequiresConfirmation() {
		t.Fatal("a large but safe change demanded confirmation, which trains users to click through prompts")
	}

	if !dangerous.RequiresConfirmation() {
		t.Fatal("deleting production customer data did not require confirmation")
	}
	if dangerous.Complexity.AtLeast(goalintake.LevelMed) {
		t.Fatalf("a one-line deletion was assessed as complexity %s; effort and danger are being conflated",
			dangerous.Complexity)
	}

	// The two must be distinguishable on the safety dimensions specifically.
	if len(complex.Elevated()) != 0 {
		t.Fatalf("a safe refactor raised safety dimensions: %v", complex.Elevated())
	}
	if len(dangerous.Elevated()) == 0 {
		t.Fatal("a destructive production change raised no safety dimension")
	}
}

// Complexity is deliberately excluded from the safety dimensions, because it
// is the one most likely to be mistaken for danger.
func TestComplexityIsNotASafetyDimension(t *testing.T) {
	assessment := assess("rewrite the architecture from scratch")
	if _, present := assessment.SafetyDimensions()["complexity"]; present {
		t.Fatal("complexity is counted as a safety dimension")
	}
	if !assessment.Complexity.AtLeast(goalintake.LevelHigh) {
		t.Fatalf("a from-scratch rewrite was complexity %s", assessment.Complexity)
	}
	if assessment.RequiresConfirmation() {
		t.Fatal("high complexity alone demanded confirmation")
	}
}

// Each dimension responds to its own evidence and not to the others.
func TestDimensionsAreIndependent(t *testing.T) {
	cases := map[string]struct {
		request   string
		dimension string
	}{
		"reach":        {"update every file across the codebase", "blast_radius"},
		"privilege":    {"run the migration as root", "privilege"},
		"irreversible": {"drop table sessions", "reversibility"},
		"external":     {"deploy the release and notify users", "external_effects"},
		"sensitive":    {"export the customer data including pii", "data_sensitivity"},
		"critical":     {"patch the production service", "operational_criticality"},
		"verification": {"fix the intermittent race condition", "verification_difficulty"},
		"dependency":   {"do this once the migration is finished, it depends on that", "dependency_depth"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assessment := goalintake.AssessRequest(tc.request, safeContext())
			dimensions := assessment.Dimensions()
			if !dimensions[tc.dimension].AtLeast(goalintake.LevelMed) {
				t.Fatalf("%q did not raise %s (got %s)", tc.request, tc.dimension, dimensions[tc.dimension])
			}
			// The finding is inspectable rather than asserted.
			if len(assessment.Signals) == 0 {
				t.Fatal("the assessment recorded no signal explaining its finding")
			}
		})
	}
}

// A routine request stays routine. If ordinary work triggered confirmation,
// the prompt would stop meaning anything.
func TestRoutineRequestNeedsNoConfirmation(t *testing.T) {
	for _, request := range []string{
		"add a unit test for the parser",
		"fix the typo in the readme",
		"add a nil check to the config loader",
	} {
		assessment := goalintake.AssessRequest(request, safeContext())
		if assessment.RequiresConfirmation() {
			t.Fatalf("a routine request required confirmation: %q (elevated: %v)",
				request, assessment.Elevated())
		}
	}
}

// A request too short to say anything is ambiguous, not simple.
func TestEmptyOrTerseRequestIsAmbiguous(t *testing.T) {
	for _, request := range []string{"", "fix", "do it", "  "} {
		assessment := goalintake.AssessRequest(request, safeContext())
		if !assessment.Ambiguity.AtLeast(goalintake.LevelHigh) {
			t.Fatalf("%q was assessed as ambiguity %s", request, assessment.Ambiguity)
		}
	}
}

// UNKNOWN is not NONE. A dimension nobody established must not be the reason a
// request looks safe.
func TestUnknownIsNotTreatedAsHarmless(t *testing.T) {
	if goalintake.LevelUnknown.Established() {
		t.Fatal("UNKNOWN reported itself as established")
	}
	if !goalintake.LevelUnknown.AtLeast(goalintake.LevelHigh) {
		t.Fatal("UNKNOWN ranked below HIGH, so an unassessed dimension would read as safer than a dangerous one")
	}

	context := safeContext()
	context.ScopeKnown = false
	assessment := goalintake.AssessRequest("update the config", context)

	if assessment.BlastRadius != goalintake.LevelUnknown {
		t.Fatalf("an unresolved scope produced blast radius %s", assessment.BlastRadius)
	}
	if !assessment.RequiresConfirmation() {
		t.Fatal("a request whose reach could not be established did not require confirmation")
	}
	if len(assessment.Unestablished()) == 0 {
		t.Fatal("the unestablished dimension was not reported")
	}
}

// Project context raises dimensions the request text cannot speak to, and only
// ever raises them.
func TestProjectContextRaisesButNeverLowers(t *testing.T) {
	request := "update the configuration file"

	safe := goalintake.AssessRequest(request, safeContext())
	if safe.RequiresConfirmation() {
		t.Fatal("a routine change in a safe project required confirmation")
	}

	production := safeContext()
	production.ProductionProject = true
	inProduction := goalintake.AssessRequest(request, production)
	if !inProduction.OperationalCriticality.AtLeast(goalintake.LevelHigh) {
		t.Fatal("a production project did not raise operational criticality")
	}
	if !inProduction.RequiresConfirmation() {
		t.Fatal("the same change in production did not require confirmation")
	}

	unrecoverable := safeContext()
	unrecoverable.Recoverable = false
	if !goalintake.AssessRequest(request, unrecoverable).Reversibility.AtLeast(goalintake.LevelHigh) {
		t.Fatal("a project with no way back did not raise irreversibility")
	}

	dirty := safeContext()
	dirty.DirtyWorktree = true
	if !goalintake.AssessRequest(request, dirty).Reversibility.AtLeast(goalintake.LevelMed) {
		t.Fatal("uncommitted work did not raise irreversibility")
	}

	// Context cannot lower a dimension the request already raised.
	dangerous := "delete the production database"
	withSafeContext := goalintake.AssessRequest(dangerous, safeContext())
	if !withSafeContext.RequiresConfirmation() {
		t.Fatal("a benign-looking context lowered a dangerous request below confirmation")
	}
}

// There is no overall score, because a single number would imply a precision
// nobody has and would hide the distinction between effort and danger.
func TestAssessmentExposesNoOverallScore(t *testing.T) {
	assessment := assess("rewrite everything and deploy to production")
	dimensions := assessment.Dimensions()
	if len(dimensions) != 10 {
		t.Fatalf("expected ten independent dimensions, got %d", len(dimensions))
	}
	for name, level := range dimensions {
		if level == "" {
			t.Fatalf("dimension %s was left unset rather than assessed", name)
		}
	}
}

// Assessment is deterministic: the same request always produces the same
// finding, so two surfaces cannot disagree about a request.
func TestAssessmentIsDeterministic(t *testing.T) {
	request := "delete the staging database and redeploy everything to production"
	first := assess(request)
	for i := 0; i < 10; i++ {
		next := assess(request)
		for name, level := range first.Dimensions() {
			if next.Dimensions()[name] != level {
				t.Fatalf("repeated assessment differed on %s", name)
			}
		}
		if len(next.Signals) != len(first.Signals) {
			t.Fatal("repeated assessment produced a different number of signals")
		}
		for j := range next.Signals {
			if next.Signals[j] != first.Signals[j] {
				t.Fatal("signal ordering is not stable")
			}
		}
	}
}

// A request combining several dangers raises each of them, rather than one
// dominating.
func TestMultipleDangersAreAllReported(t *testing.T) {
	assessment := assess("as root, delete the production customer database and notify users")
	for _, dimension := range []string{
		"privilege", "reversibility", "external_effects", "data_sensitivity", "operational_criticality",
	} {
		if !assessment.Dimensions()[dimension].AtLeast(goalintake.LevelMed) {
			t.Fatalf("%s was not raised by a request that clearly implicates it", dimension)
		}
	}
	if len(assessment.Elevated()) < 4 {
		t.Fatalf("only %d safety dimensions were elevated: %v", len(assessment.Elevated()), assessment.Elevated())
	}
}
