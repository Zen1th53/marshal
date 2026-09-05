package model_test

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestGoalContractValidation(t *testing.T) {
	g := model.GoalContract{
		ID:              "goal-1",
		SessionID:       "sess-1",
		Revision:        1,
		DesiredOutcome:  "Implement feature X",
		Risk:            model.R1,
		AuthoritySource: "operator:zen1th",
	}
	if err := model.ValidateGoal(g); err != nil {
		t.Fatalf("expected valid goal, got: %v", err)
	}

	invalid := g
	invalid.DesiredOutcome = ""
	if err := model.ValidateGoal(invalid); err == nil {
		t.Fatal("expected error on empty desired outcome")
	}

	invalidRev := g
	invalidRev.Revision = 0
	if err := model.ValidateGoal(invalidRev); err == nil {
		t.Fatal("expected error on revision < 1")
	}
}

func TestGoalUnderstandingState(t *testing.T) {
	g := model.GoalContract{
		ID:              "goal-1",
		SessionID:       "sess-1",
		Revision:        1,
		DesiredOutcome:  "Refactor auth logic",
		Risk:            model.R2,
		AuthoritySource: "operator:zen1th",
	}

	// 1. Initial clear intent -> READY
	if state := g.EvaluateUnderstanding(); state != model.GoalReady {
		t.Fatalf("expected READY, got: %s", state)
	}

	// 2. Low-risk reversible assumption -> stays READY
	g.Assumptions = append(g.Assumptions, model.Assumption{
		ID:           "a1",
		Text:         "Use standard sha256 for internal hashes",
		Risk:         "low",
		IsReversible: true,
	})
	if state := g.EvaluateUnderstanding(); state != model.GoalReady {
		t.Fatalf("expected READY with low-risk assumption, got: %s", state)
	}

	// 3. High-risk or irreversible assumption -> transitions to NEEDS_INPUT
	g.Assumptions = append(g.Assumptions, model.Assumption{
		ID:           "a2",
		Text:         "Assume DB migration can wipe existing test tables",
		Risk:         "high",
		IsReversible: false,
	})
	if state := g.EvaluateUnderstanding(); state != model.GoalNeedsInput {
		t.Fatalf("expected NEEDS_INPUT with high-risk assumption, got: %s", state)
	}

	// 4. Unresolved decision -> NEEDS_INPUT
	g.Assumptions = g.Assumptions[:1] // reset assumptions
	g.UnresolvedDecisions = append(g.UnresolvedDecisions, model.UnresolvedDecision{
		ID:           "d1",
		Question:     "Which cryptographic curve should be used for identity?",
		RequiresUser: true,
	})
	if state := g.EvaluateUnderstanding(); state != model.GoalNeedsInput {
		t.Fatalf("expected NEEDS_INPUT with unresolved decision, got: %s", state)
	}
}

func TestGoalDiff(t *testing.T) {
	oldGoal := model.GoalContract{
		ID:             "goal-1",
		Revision:       1,
		DesiredOutcome: "Build initial auth service",
		Scope:          []string{"internal/auth"},
		Constraints: []model.Constraint{
			{ID: "c1", Text: "Must use Argon2id", IsHard: true},
			{ID: "c2", Text: "Max 100ms response", IsHard: false},
		},
		DoNotDo:         []string{"Never log passwords in plaintext"},
		SuccessCriteria: []string{"Unit tests pass"},
	}

	newGoal := model.GoalContract{
		ID:             "goal-1",
		Revision:       2,
		DesiredOutcome: "Build initial auth service with mTLS",
		Scope:          []string{"internal/auth", "internal/tls"},
		Constraints: []model.Constraint{
			{ID: "c1", Text: "Must use Argon2id", IsHard: true},
			{ID: "c2", Text: "Max 50ms response", IsHard: false}, // modified
			{ID: "c3", Text: "Require mTLS client certs", IsHard: true}, // added
		},
		DoNotDo:         []string{"Never log passwords in plaintext", "Do not accept TLS 1.1"}, // added dnd
		SuccessCriteria: []string{"Unit tests pass", "Integration tests pass"},
	}

	diff := model.ComputeGoalDiff(oldGoal, newGoal)
	if !diff.RequiresReEvaluation {
		t.Fatal("expected RequiresReEvaluation = true")
	}
	if len(diff.AddedConstraints) != 1 || diff.AddedConstraints[0].ID != "c3" {
		t.Fatalf("unexpected added constraints: %+v", diff.AddedConstraints)
	}
	if len(diff.ModifiedConstraints) != 1 || diff.ModifiedConstraints[0].ID != "c2" {
		t.Fatalf("unexpected modified constraints: %+v", diff.ModifiedConstraints)
	}
	if len(diff.AddedDoNotDo) != 1 || diff.AddedDoNotDo[0] != "Do not accept TLS 1.1" {
		t.Fatalf("unexpected added do_not_do: %+v", diff.AddedDoNotDo)
	}
	if !diff.ScopeChanged {
		t.Fatal("expected ScopeChanged = true")
	}
	if !diff.SuccessCriteriaChanged {
		t.Fatal("expected SuccessCriteriaChanged = true")
	}
	if diff.SafestRollbackRevision != 1 {
		t.Fatalf("expected rollback revision 1, got %d", diff.SafestRollbackRevision)
	}
}

func TestHardConstraintSecurityInvariant(t *testing.T) {
	oldGoal := model.GoalContract{
		ID:       "goal-sec",
		Revision: 1,
		Constraints: []model.Constraint{
			{ID: "c-immutable", Text: "Zero egress outside approved IP list", IsHard: true},
			{ID: "c-soft", Text: "Prefer Go standard library", IsHard: false},
		},
		DoNotDo: []string{"Do not disable TLS verification"},
	}

	// 1. Agent tries to remove hard constraint
	agentGoalRemoved := oldGoal
	agentGoalRemoved.Revision = 2
	agentGoalRemoved.Constraints = []model.Constraint{
		{ID: "c-soft", Text: "Prefer Go standard library", IsHard: false},
	}
	if err := model.CanModifyGoal("developer", oldGoal, agentGoalRemoved); err == nil {
		t.Fatal("expected agent removal of hard constraint to fail")
	}

	// 2. Agent tries to demote hard constraint to soft
	agentGoalDemoted := oldGoal
	agentGoalDemoted.Revision = 2
	agentGoalDemoted.Constraints = []model.Constraint{
		{ID: "c-immutable", Text: "Zero egress outside approved IP list", IsHard: false},
		{ID: "c-soft", Text: "Prefer Go standard library", IsHard: false},
	}
	if err := model.CanModifyGoal("developer", oldGoal, agentGoalDemoted); err == nil {
		t.Fatal("expected agent demotion of hard constraint to fail")
	}

	// 3. Agent tries to remove DoNotDo constraint
	agentGoalNoDND := oldGoal
	agentGoalNoDND.Revision = 2
	agentGoalNoDND.DoNotDo = nil
	if err := model.CanModifyGoal("developer", oldGoal, agentGoalNoDND); err == nil {
		t.Fatal("expected agent removal of DoNotDo to fail")
	}

	// 4. Operator legitimately updates constraints
	if err := model.CanModifyGoal("operator", oldGoal, agentGoalRemoved); err != nil {
		t.Fatalf("operator must be allowed to modify constraints, got error: %v", err)
	}

	// 5. Agent modifies soft constraint legitimately
	agentSoftGoal := oldGoal
	agentSoftGoal.Revision = 2
	agentSoftGoal.Constraints = []model.Constraint{
		{ID: "c-immutable", Text: "Zero egress outside approved IP list", IsHard: true},
		{ID: "c-soft", Text: "Prefer Go standard library or google packages", IsHard: false},
	}
	if err := model.CanModifyGoal("developer", oldGoal, agentSoftGoal); err != nil {
		t.Fatalf("agent should be allowed to modify soft constraint, got: %v", err)
	}
}

func BenchmarkGoalDiff(b *testing.B) {
	g1 := model.GoalContract{
		ID:          "g",
		Revision:    1,
		Constraints: []model.Constraint{{ID: "c1", Text: "text", IsHard: true}},
		DoNotDo:     []string{"d1"},
	}
	g2 := model.GoalContract{
		ID:          "g",
		Revision:    2,
		Constraints: []model.Constraint{{ID: "c1", Text: "text", IsHard: true}, {ID: "c2", Text: "t2"}},
		DoNotDo:     []string{"d1", "d2"},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = model.ComputeGoalDiff(g1, g2)
	}
}
