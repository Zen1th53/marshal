package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestGoalContractPersistenceAndRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "marshal_goal.db")

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	goal := model.GoalContract{
		ID:               "goal-alpha",
		SessionID:        "session-100",
		Revision:         1,
		DesiredOutcome:   "Deliver MARSHAL v1.5.0 with full verification",
		ExpectedArtifact: "bin/marshal binary and verified release bundle",
		Scope:            []string{"internal/store", "internal/model", "internal/tui"},
		Constraints: []model.Constraint{
			{ID: "c-1", Text: "No fake completion", Source: "operator", IsHard: true, Scope: "global"},
			{ID: "c-2", Text: "Fail-closed security writes", Source: "policy", IsHard: true, Scope: "security"},
		},
		DoNotDo:         []string{"Do not bypass approval gates", "Do not discard user work"},
		SuccessCriteria: []string{"All 15 E2E scenarios pass", "Coverage meets threshold"},
		Risk:            model.R2,
		AuthoritySource: "operator:zen1th",
		BudgetRef:       "budget-100",
		RequiredCriticalClaims: []string{
			"claim.security.fail_closed",
			"claim.persistence.durable",
		},
		UnderstandingState: model.GoalReady,
		UnresolvedDecisions: nil,
		Assumptions: []model.Assumption{
			{ID: "a-1", Text: "Local environment has Go 1.27 installed", Risk: "low", IsReversible: true, CreatedBy: "operator"},
		},
		RepoCommit: "f49d7a3",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}

	// 1. Save Goal revision 1
	if err := st.SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatalf("save goal revision 1: %v", err)
	}

	// 2. Query active goal
	active, err := st.GetActiveGoalContract(ctx, "session-100")
	if err != nil {
		t.Fatalf("get active goal: %v", err)
	}
	if active.ID != goal.ID || active.Revision != 1 || active.DesiredOutcome != goal.DesiredOutcome {
		t.Fatalf("active goal mismatch: got %+v, want %+v", active, goal)
	}

	// 3. Close store and simulate restart
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// 4. Re-open store from disk
	reopenedSt, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopenedSt.Close()

	reloaded, err := reopenedSt.GetActiveGoalContract(ctx, "session-100")
	if err != nil {
		t.Fatalf("reload active goal after restart: %v", err)
	}
	if reloaded.ID != goal.ID || reloaded.Revision != 1 {
		t.Fatalf("reloaded goal mismatch: %+v", reloaded)
	}
	if len(reloaded.Constraints) != 2 || !reloaded.Constraints[0].IsHard {
		t.Fatalf("reloaded constraints corrupted: %+v", reloaded.Constraints)
	}
	if len(reloaded.DoNotDo) != 2 {
		t.Fatalf("reloaded do_not_do corrupted: %+v", reloaded.DoNotDo)
	}
}

func TestGoalContractCASVersioning(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "marshal_cas.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	g1 := model.GoalContract{
		ID:              "goal-cas",
		SessionID:       "sess-cas",
		DesiredOutcome:  "Step 1 outcome",
		Risk:            model.R1,
		AuthoritySource: "operator:zen1th",
	}

	// 1. Initial save with expectedRevision 0
	if err := st.SaveGoalContract(ctx, g1, 0); err != nil {
		t.Fatalf("save initial goal: %v", err)
	}

	// Duplicate initial save must fail with conflict
	if err := st.SaveGoalContract(ctx, g1, 0); !errors.Is(err, model.ErrGoalConflict) {
		t.Fatalf("expected ErrGoalConflict on duplicate initial save, got %v", err)
	}

	// 2. Advance to Revision 2 with expectedRevision = 1
	g2 := g1
	g2.DesiredOutcome = "Step 2 outcome updated"
	if err := st.SaveGoalContract(ctx, g2, 1); err != nil {
		t.Fatalf("save revision 2: %v", err)
	}

	active, err := st.GetActiveGoalContract(ctx, "sess-cas")
	if err != nil {
		t.Fatalf("get active goal: %v", err)
	}
	if active.Revision != 2 || active.DesiredOutcome != "Step 2 outcome updated" {
		t.Fatalf("expected revision 2, got: %+v", active)
	}

	// 3. Stale revision update attempt (CAS conflict: expectedRevision 1 when current is 2)
	staleGoal := g1
	staleGoal.DesiredOutcome = "Conflicting edit"
	err = st.SaveGoalContract(ctx, staleGoal, 1)
	if !errors.Is(err, model.ErrGoalConflict) {
		t.Fatalf("expected ErrGoalConflict on stale CAS update, got: %v", err)
	}

	// 4. List revisions
	revs, err := st.ListGoalRevisions(ctx, "goal-cas")
	if err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	if len(revs) != 2 {
		t.Fatalf("expected 2 revisions, got %d", len(revs))
	}
	if revs[0].Revision != 1 || revs[1].Revision != 2 {
		t.Fatalf("revisions out of order: %+v", revs)
	}
}

func TestGoalContractActivePointerAndRollback(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "marshal_active.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	g := model.GoalContract{
		ID:              "goal-rb",
		SessionID:       "sess-rb",
		DesiredOutcome:  "Base state",
		Risk:            model.R1,
		AuthoritySource: "operator:zen1th",
	}

	if err := st.SaveGoalContract(ctx, g, 0); err != nil {
		t.Fatalf("save rev 1: %v", err)
	}

	g.DesiredOutcome = "Divergent state"
	if err := st.SaveGoalContract(ctx, g, 1); err != nil {
		t.Fatalf("save rev 2: %v", err)
	}

	// Verify active is 2
	active, err := st.GetActiveGoalContract(ctx, "sess-rb")
	if err != nil || active.Revision != 2 {
		t.Fatalf("expected active revision 2, got: %v, %+v", err, active)
	}

	// Safely roll back active goal to revision 1
	if err := st.SetActiveGoalContract(ctx, "sess-rb", "goal-rb", 1); err != nil {
		t.Fatalf("rollback active to rev 1: %v", err)
	}

	rolledBack, err := st.GetActiveGoalContract(ctx, "sess-rb")
	if err != nil {
		t.Fatalf("get active goal after rollback: %v", err)
	}
	if rolledBack.Revision != 1 || rolledBack.DesiredOutcome != "Base state" {
		t.Fatalf("expected active revision 1, got: %+v", rolledBack)
	}

	// Non-existent revision must fail
	if err := st.SetActiveGoalContract(ctx, "sess-rb", "goal-rb", 999); !errors.Is(err, model.ErrGoalNotFound) {
		t.Fatalf("expected ErrGoalNotFound for nonexistent revision, got %v", err)
	}
}
