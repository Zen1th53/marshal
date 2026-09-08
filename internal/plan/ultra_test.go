package plan_test

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/plan"
)

func proposal(id string, tasks ...plan.Task) plan.Proposal {
	return plan.Proposal{
		ID:     id,
		Source: plan.ProposalSource{Provider: "provider-" + id, Model: "model"},
		Tasks:  tasks,
	}
}

func ultraRequest(proposals ...plan.Proposal) plan.UltraRequest {
	return plan.UltraRequest{
		Proposals: proposals,
		Criteria:  []string{"responses are cached", "existing tests pass"},
		HardConstraints: []string{
			"Do not modify the database schema",
		},
	}
}

// The plan that does more of the job wins, even against a tidier one.
func TestBestPlanIsTheOneThatCoversTheGoal(t *testing.T) {
	thorough := proposal("thorough",
		plan.Task{ID: "a", Title: "add the cache", Mutating: true, Weight: 1,
			Criteria: []string{"responses are cached"}},
		plan.Task{ID: "b", Title: "run the tests", Weight: 1,
			Criteria: []string{"existing tests pass"}},
	)
	partial := proposal("partial",
		plan.Task{ID: "a", Title: "add the cache", Mutating: true, Weight: 1,
			Criteria: []string{"responses are cached"}},
	)

	selection := plan.SelectBest(ultraRequest(thorough, partial))
	if !selection.Chosen() {
		t.Fatal("no proposal was selected")
	}
	if selection.Best != "thorough" {
		t.Fatalf("selected %q; the plan covering more of the goal should win", selection.Best)
	}
	// The reason cites figures rather than an adjective.
	if !strings.Contains(selection.Reason, "2") {
		t.Fatalf("the reason does not cite what was covered: %q", selection.Reason)
	}

	// Coverage must be the key that decides, not merely a key that agrees with
	// whichever one does. Here the fuller plan is worse on every later
	// dimension — more irreversible work, more exposure, more collisions —
	// so it can only win if coverage is ranked first.
	awkward := proposal("awkward",
		plan.Task{ID: "a", Title: "delete the stale entries", Mutating: true, Weight: 1,
			Paths: []string{"internal/api"}, Criteria: []string{"responses are cached"}},
		plan.Task{ID: "b", Title: "drop the old index", Mutating: true, Weight: 1,
			Paths: []string{"internal/api"}, Criteria: []string{"existing tests pass"}},
	)
	tidy := proposal("tidy",
		plan.Task{ID: "a", Title: "add the cache", Mutating: true, Weight: 1,
			Criteria: []string{"responses are cached"}},
	)

	byCoverage := plan.SelectBest(ultraRequest(awkward, tidy))
	if byCoverage.Best != "awkward" {
		t.Fatalf("selected %q; a plan covering both criteria must beat a tidier one covering one, "+
			"so coverage is the deciding key rather than a coincidence", byCoverage.Best)
	}
}

// A planner's opinion of its own plan is not evidence. This is the invariant
// that stops the best marketer winning instead of the best plan.
func TestSelfScoresAreNotCanonicalEvidence(t *testing.T) {
	weak := proposal("weak",
		plan.Task{ID: "a", Title: "add the cache", Mutating: true, Weight: 1,
			Criteria: []string{"responses are cached"}},
	)
	weak.Source.SelfScore = "10/10 — optimal, highest confidence, best possible plan"

	strong := proposal("strong",
		plan.Task{ID: "a", Title: "add the cache", Mutating: true, Weight: 1,
			Criteria: []string{"responses are cached"}},
		plan.Task{ID: "b", Title: "run the tests", Weight: 1,
			Criteria: []string{"existing tests pass"}},
	)
	strong.Source.SelfScore = "3/10 — probably inadequate"

	selection := plan.SelectBest(ultraRequest(weak, strong))
	if selection.Best != "strong" {
		t.Fatalf("selected %q; a self-declared score changed the outcome", selection.Best)
	}

	// The claim is preserved for a reader but never scored.
	for _, comparison := range selection.Comparisons {
		if strings.Contains(strings.Join(comparison.Reasons, " "), "10/10") {
			t.Fatal("a self-score reached the comparison's reasoning")
		}
	}
}

// A plan that breaches a hard constraint is disqualified, not penalised. A
// plan that does the forbidden thing efficiently is not a better plan.
func TestConstraintBreachDisqualifiesRatherThanCosts(t *testing.T) {
	breaching := proposal("breaching",
		plan.Task{ID: "a", Title: "modify the database schema", Mutating: true, Weight: 1,
			Paths:    []string{"internal/store/schema.sql"},
			Criteria: []string{"responses are cached", "existing tests pass"}},
	)
	compliant := proposal("compliant",
		plan.Task{ID: "a", Title: "add the cache", Mutating: true, Weight: 1,
			Criteria: []string{"responses are cached"}},
	)

	selection := plan.SelectBest(ultraRequest(breaching, compliant))
	if selection.Best == "breaching" {
		t.Fatal("a plan breaching a hard constraint was selected despite covering more")
	}
	if selection.Best != "compliant" {
		t.Fatalf("selected %q, want the compliant plan", selection.Best)
	}

	for _, comparison := range selection.Comparisons {
		if comparison.ProposalID != "breaching" {
			continue
		}
		if !comparison.Disqualified {
			t.Fatal("the breaching plan was not disqualified")
		}
		if !strings.Contains(strings.Join(comparison.Reasons, " "), "database schema") {
			t.Fatalf("the disqualification does not name the constraint: %v", comparison.Reasons)
		}
	}
}

// A constraint's distinctive words must all appear, so a task merely
// mentioning a subject is not flagged.
func TestConstraintMatchingDoesNotFireOnMereMention(t *testing.T) {
	adjacent := proposal("adjacent",
		plan.Task{ID: "a", Title: "read the database configuration", Weight: 1,
			Criteria: []string{"responses are cached", "existing tests pass"}},
	)

	selection := plan.SelectBest(ultraRequest(adjacent))
	if !selection.Chosen() {
		t.Fatalf("a task merely mentioning a constrained subject was disqualified: %+v",
			selection.Comparisons)
	}
}

// Work routed somewhere ungovernable disqualifies the proposal.
func TestUngovernedRoutingDisqualifies(t *testing.T) {
	request := ultraRequest(
		proposal("rogue", plan.Task{ID: "a", Title: "add the cache", Mutating: true,
			Weight: 1, Role: "rogue-provider",
			Criteria: []string{"responses are cached", "existing tests pass"}}),
		proposal("governed", plan.Task{ID: "a", Title: "add the cache", Mutating: true,
			Weight: 1, Role: "developer", Criteria: []string{"responses are cached"}}),
	)
	request.Ungovernable = map[string]bool{"rogue-provider": true}

	selection := plan.SelectBest(request)
	if selection.Best != "governed" {
		t.Fatalf("selected %q; work routed somewhere ungovernable should be disqualified", selection.Best)
	}
}

// A plan exceeding the budget is disqualified rather than trimmed, because
// trimming would silently drop work the planner thought necessary.
func TestOverBudgetPlanIsDisqualified(t *testing.T) {
	request := ultraRequest(
		proposal("large",
			plan.Task{ID: "a", Title: "a", Weight: 1, Criteria: []string{"responses are cached"}},
			plan.Task{ID: "b", Title: "b", Weight: 1, Criteria: []string{"existing tests pass"}},
			plan.Task{ID: "c", Title: "c", Weight: 1},
		),
		proposal("small",
			plan.Task{ID: "a", Title: "a", Weight: 1, Criteria: []string{"responses are cached"}},
		),
	)
	request.MaxTasks = 2

	selection := plan.SelectBest(request)
	if selection.Best != "small" {
		t.Fatalf("selected %q, want the plan that fits the budget", selection.Best)
	}
}

// When nothing qualifies, the refusal says so rather than picking the least
// bad option.
func TestNoUsableProposalIsRefusedNotApproximated(t *testing.T) {
	request := ultraRequest(
		proposal("bad", plan.Task{ID: "a", Title: "modify the database schema",
			Mutating: true, Weight: 1, Paths: []string{"schema"}}),
	)

	selection := plan.SelectBest(request)
	if selection.Chosen() {
		t.Fatalf("a disqualified proposal was selected: %q", selection.Best)
	}
	if strings.TrimSpace(selection.Reason) == "" {
		t.Fatal("the refusal gives no reason")
	}
}

// A losing proposal can still hold the one thing everyone else missed, and
// that gap is named rather than spliced in silently.
func TestLosingProposalStrengthsAreNamed(t *testing.T) {
	// Each proposal covers one criterion the other does not, so neither is
	// strictly better and the stable tie-break decides. Whichever wins, the
	// criterion it misses must be named.
	cache := proposal("a-cache",
		plan.Task{ID: "a", Title: "add the cache", Mutating: true, Weight: 1,
			Criteria: []string{"responses are cached"}},
	)
	tests := proposal("b-tests",
		plan.Task{ID: "b", Title: "run the tests", Weight: 1,
			Criteria: []string{"existing tests pass"}},
	)

	selection := plan.SelectBest(plan.UltraRequest{
		Proposals: []plan.Proposal{cache, tests},
		Criteria:  []string{"responses are cached", "existing tests pass"},
	})
	if !selection.Chosen() {
		t.Fatal("no proposal was selected")
	}
	if len(selection.MergedStrengths) == 0 {
		t.Fatal("a criterion only the other plan covered was not surfaced")
	}

	// The named gap is the criterion the winner does not cover, and it credits
	// the proposal that does.
	missing := "existing tests pass"
	other := "b-tests"
	if selection.Best == "b-tests" {
		missing, other = "responses are cached", "a-cache"
	}
	joined := strings.Join(selection.MergedStrengths, " ")
	if !strings.Contains(joined, missing) {
		t.Fatalf("the winner %q does not cover %q, but that gap is not named: %v",
			selection.Best, missing, selection.MergedStrengths)
	}
	if !strings.Contains(joined, other) {
		t.Fatalf("the gap does not credit the proposal that covers it: %v", selection.MergedStrengths)
	}
}

// Comparison is deterministic: the same proposals always produce the same
// winner and the same figures.
func TestComparisonIsDeterministic(t *testing.T) {
	request := ultraRequest(
		proposal("b", plan.Task{ID: "x", Title: "add the cache", Mutating: true, Weight: 1,
			Criteria: []string{"responses are cached"}}),
		proposal("a", plan.Task{ID: "y", Title: "add the cache", Mutating: true, Weight: 1,
			Criteria: []string{"responses are cached"}}),
	)

	first := plan.SelectBest(request)
	for i := 0; i < 10; i++ {
		next := plan.SelectBest(request)
		if next.Best != first.Best {
			t.Fatal("repeated selection produced different winners")
		}
		if len(next.Comparisons) != len(first.Comparisons) {
			t.Fatal("repeated selection produced different comparisons")
		}
		for j := range next.Comparisons {
			if next.Comparisons[j].ProposalID != first.Comparisons[j].ProposalID ||
				next.Comparisons[j].GoalCoverage != first.Comparisons[j].GoalCoverage {
				t.Fatal("repeated comparison produced different figures")
			}
		}
	}
	// Equal proposals tie-break on identity rather than input order.
	if first.Best != "a" {
		t.Fatalf("tied proposals selected %q; the tie-break should be stable", first.Best)
	}
}

// Every proposal is scored, including the ones that lose, so the choice can be
// inspected rather than taken on trust.
func TestEveryProposalIsScoredAndInspectable(t *testing.T) {
	selection := plan.SelectBest(ultraRequest(
		proposal("one", plan.Task{ID: "a", Title: "add the cache", Mutating: true, Weight: 1,
			Criteria: []string{"responses are cached"}}),
		proposal("two", plan.Task{ID: "b", Title: "run the tests", Weight: 1,
			Criteria: []string{"existing tests pass"}}),
	))

	if len(selection.Comparisons) != 2 {
		t.Fatalf("%d proposals were scored, want 2", len(selection.Comparisons))
	}
	for _, comparison := range selection.Comparisons {
		if comparison.ProposalID == "" {
			t.Fatal("a comparison names no proposal")
		}
	}
}
