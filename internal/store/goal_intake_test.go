package store

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func intakeGoal() model.GoalContract {
	return model.GoalContract{
		ID:                  "GOAL-intake",
		SessionID:           "SESSION-1",
		ProjectID:           "PROJECT-0123456789abcdef0123456789abcdef",
		OriginalRequest:     "Add caching to the API. Do not modify the database schema.",
		RequestDigest:       "sha256:abc123",
		ConstitutionVersion: "1.0.0",
		Confirmation:        model.ConfirmationApproved,
		Assessment: map[string]string{
			"complexity": "MEDIUM", "blast_radius": "LOW", "reversibility": "NONE",
		},
		RevisionReason:  "initial formation",
		AdvisoryUsed:    true,
		DesiredOutcome:  "API responses are cached",
		Risk:            model.R1,
		AuthoritySource: "operator",
		Constraints: []model.Constraint{{
			ID: "CONSTRAINT-1", Text: "Do not modify the database schema",
			Source: "user", IsHard: true,
		}},
	}
}

// Goal intake must survive a round trip. Everything Process 03 establishes —
// the user's own words, the project binding, the assessment, the confirmation
// state — is worthless if it does not come back.
func TestGoalIntakeSurvivesARoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	original := intakeGoal()
	if err := st.SaveGoalContract(ctx, original, 0); err != nil {
		t.Fatalf("save: %v", err)
	}

	restored, err := st.GetActiveGoalContract(ctx, original.SessionID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	if restored.OriginalRequest != original.OriginalRequest {
		t.Fatalf("the user's own words did not survive:\n got: %q\nwant: %q",
			restored.OriginalRequest, original.OriginalRequest)
	}
	if restored.ProjectID != original.ProjectID {
		t.Fatalf("the project binding did not survive: %q", restored.ProjectID)
	}
	if restored.RequestDigest != original.RequestDigest {
		t.Fatalf("the request digest did not survive: %q", restored.RequestDigest)
	}
	if restored.ConstitutionVersion != original.ConstitutionVersion {
		t.Fatalf("the constitution version did not survive: %q", restored.ConstitutionVersion)
	}
	if restored.Confirmation != model.ConfirmationApproved {
		t.Fatalf("the confirmation state did not survive: %q", restored.Confirmation)
	}
	if !restored.Confirmation.Settled() {
		t.Fatal("a restored approved Goal did not permit planning")
	}
	if !restored.AdvisoryUsed {
		t.Fatal("the record that a model contributed did not survive")
	}
	if restored.RevisionReason != original.RevisionReason {
		t.Fatalf("the revision reason did not survive: %q", restored.RevisionReason)
	}
	if len(restored.Assessment) != len(original.Assessment) {
		t.Fatalf("the assessment did not survive: %+v", restored.Assessment)
	}
	for dimension, level := range original.Assessment {
		if restored.Assessment[dimension] != level {
			t.Fatalf("assessment dimension %s came back as %q, want %q",
				dimension, restored.Assessment[dimension], level)
		}
	}
	if len(restored.Constraints) != 1 || !restored.Constraints[0].IsHard {
		t.Fatalf("the hard constraint did not survive: %+v", restored.Constraints)
	}
}

// A delegated Goal comes back as delegated, never as approved. The two are
// different claims when a result is reviewed later.
func TestDelegatedGoalDoesNotRestoreAsApproved(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	goal := intakeGoal()
	goal.Confirmation = model.ConfirmationDelegated
	if err := st.SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatalf("save: %v", err)
	}
	restored, err := st.GetActiveGoalContract(ctx, goal.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Confirmation != model.ConfirmationDelegated {
		t.Fatalf("a delegated Goal restored as %q", restored.Confirmation)
	}
	if restored.Confirmation == model.ConfirmationApproved {
		t.Fatal("a delegated decision is indistinguishable from one a person made")
	}
}

// A Goal saved with no confirmation is pending, not settled. Defaulting the
// other way would let an unconfirmed Goal permit planning.
func TestUnsetConfirmationDefaultsToPending(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	goal := intakeGoal()
	goal.Confirmation = ""
	if err := st.SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatalf("save: %v", err)
	}
	restored, err := st.GetActiveGoalContract(ctx, goal.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Confirmation != model.ConfirmationPending {
		t.Fatalf("an unconfirmed Goal restored as %q", restored.Confirmation)
	}
	if restored.Confirmation.Settled() {
		t.Fatal("an unconfirmed Goal permitted planning")
	}
}

// Revisions preserve the original request and stay CAS-safe. This is where
// drift would accumulate if the original were rewritten each time.
func TestRevisionsPreserveTheOriginalRequestUnderCAS(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	original := intakeGoal()
	if err := st.SaveGoalContract(ctx, original, 0); err != nil {
		t.Fatalf("save: %v", err)
	}

	for revision := int64(1); revision <= 4; revision++ {
		current, err := st.GetActiveGoalContract(ctx, original.SessionID)
		if err != nil {
			t.Fatalf("read revision %d: %v", revision, err)
		}
		current.DesiredOutcome = "refined outcome"
		current.RevisionReason = "further refinement"
		if err := st.SaveGoalContract(ctx, current, revision); err != nil {
			t.Fatalf("revise at %d: %v", revision, err)
		}
	}

	final, err := st.GetActiveGoalContract(ctx, original.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if final.OriginalRequest != original.OriginalRequest {
		t.Fatalf("the original request eroded over revisions:\n got: %q\nwant: %q",
			final.OriginalRequest, original.OriginalRequest)
	}
	if final.RequestDigest != original.RequestDigest {
		t.Fatal("the binding to the original request text was lost over revisions")
	}
	if final.Revision != 5 {
		t.Fatalf("revision is %d, want 5", final.Revision)
	}

	// A stale expected revision is refused rather than silently overwriting.
	stale := final
	stale.DesiredOutcome = "a conflicting edit"
	if err := st.SaveGoalContract(ctx, stale, 1); err == nil {
		t.Fatal("a stale revision overwrote a newer Goal")
	}
}

// The schema refuses a confirmation state MARSHAL cannot produce, so a direct
// write cannot mark a Goal approved in a way the code never would.
func TestStoredConfirmationStateIsConstrained(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	// The model refuses it first.
	goal := intakeGoal()
	goal.Confirmation = model.ConfirmationState("AUTO_APPROVED")
	if err := st.SaveGoalContract(ctx, goal, 0); err == nil {
		t.Fatal("a Goal with an invented confirmation state was saved")
	}

	// And the schema refuses it too, so a direct write cannot get past it.
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO goal_contracts(
			goal_id, session_id, revision, desired_outcome, expected_artifact,
			risk, authority_source, understanding_state, confirmation_state,
			created_at, updated_at)
		VALUES('GOAL-direct', 'SESSION-2', 1, 'outcome', 'artifact', 'R1',
			'operator', 'READY', 'AUTO_APPROVED',
			'2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
	`); err == nil {
		t.Fatal("the schema accepted an invented confirmation state")
	}
}

// A Goal written before this migration keeps working, with no original request
// recorded and a pending confirmation. Losing such rows would be worse than
// admitting what was never captured.
func TestPreMigrationGoalsRemainReadable(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO goal_contracts(
			goal_id, session_id, revision, desired_outcome, expected_artifact,
			risk, authority_source, understanding_state, created_at, updated_at)
		VALUES('GOAL-legacy', 'SESSION-legacy', 1, 'an earlier goal', 'a result',
			'R1', 'operator', 'READY', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
	`); err != nil {
		t.Fatalf("insert a pre-migration goal: %v", err)
	}

	restored, err := st.GetGoalContract(ctx, "GOAL-legacy", 1)
	if err != nil {
		t.Fatalf("a pre-migration goal became unreadable: %v", err)
	}
	if restored.DesiredOutcome != "an earlier goal" {
		t.Fatalf("the upgrade altered an existing goal: %q", restored.DesiredOutcome)
	}
	if restored.OriginalRequest != "" {
		t.Fatalf("a goal with no recorded request reported one: %q", restored.OriginalRequest)
	}
	if restored.Confirmation != model.ConfirmationPending {
		t.Fatalf("a pre-migration goal defaulted to %q; it must not read as approved",
			restored.Confirmation)
	}
}
