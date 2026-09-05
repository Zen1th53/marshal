package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestBudgetPersistenceAndRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "marshal_budget_test.db")

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	sessionID := "sess-bgt-1"
	goalID := "goal-bgt-1"
	var revision int64 = 1

	toks := int64(14200)
	cost := 0.045
	consumed := model.ConsumedBudget{
		TotalTokens:       &toks,
		CostUSD:           &cost,
		Duration:          15 * time.Second,
		ModelCalls:        5,
		Handoffs:          2,
		Retries:           1,
		HasReportedTokens: true,
		HasReportedCost:   true,
	}

	// 1. Save and retrieve budget tracker
	if err := st.SaveBudgetTracker(ctx, sessionID, goalID, revision, consumed); err != nil {
		t.Fatalf("SaveBudgetTracker failed: %v", err)
	}

	retrieved, err := st.GetBudgetTracker(ctx, sessionID, goalID, revision)
	if err != nil {
		t.Fatalf("GetBudgetTracker failed: %v", err)
	}
	if retrieved == nil || *retrieved.TotalTokens != 14200 || *retrieved.CostUSD != 0.045 || retrieved.ModelCalls != 5 {
		t.Fatalf("unexpected retrieved budget: %+v", retrieved)
	}

	// 2. Save GoalTermination
	term := model.GoalTermination{
		SessionID:      sessionID,
		GoalID:         goalID,
		GoalRevision:   revision,
		State:          model.StatePartial,
		ReasonCode:     model.ReasonBudgetExhaustedTokens,
		ReasonDetail:   "Token limit 10000 reached",
		ConsumedBudget: consumed,
		CheckpointID:   "ckpt-bgt-001",
		CompletedAt:    time.Now().UTC().Truncate(time.Millisecond),
	}

	if err := st.SaveGoalTermination(ctx, term); err != nil {
		t.Fatalf("SaveGoalTermination failed: %v", err)
	}

	// 3. Restart store (close and re-open)
	if err := st.Close(); err != nil {
		t.Fatalf("failed to close store: %v", err)
	}

	st2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopening store failed: %v", err)
	}
	defer st2.Close()

	// 4. Verify data survived restart
	termRetrieved, err := st2.GetGoalTermination(ctx, sessionID, goalID, revision)
	if err != nil {
		t.Fatalf("GetGoalTermination after restart failed: %v", err)
	}
	if termRetrieved.State != model.StatePartial {
		t.Errorf("expected state PARTIAL, got %v", termRetrieved.State)
	}
	if termRetrieved.ReasonCode != model.ReasonBudgetExhaustedTokens {
		t.Errorf("expected reason %v, got %v", model.ReasonBudgetExhaustedTokens, termRetrieved.ReasonCode)
	}
	if termRetrieved.CheckpointID != "ckpt-bgt-001" {
		t.Errorf("expected checkpoint ckpt-bgt-001, got %v", termRetrieved.CheckpointID)
	}
	if termRetrieved.ConsumedBudget.TotalTokens == nil || *termRetrieved.ConsumedBudget.TotalTokens != 14200 {
		t.Errorf("expected tokens 14200, got %v", termRetrieved.ConsumedBudget.TotalTokens)
	}
}
