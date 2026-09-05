package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestBlindInterpretationStoreAndRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "marshal_interp_test.db")

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	sessionID := "sess-interp-1"
	goalID := "goal-interp-1"
	var revision int64 = 1

	now := time.Now().UTC().Truncate(time.Millisecond)

	interp1 := model.Interpretation{
		ID:           "interp-claude-1",
		GoalID:       goalID,
		GoalRevision: revision,
		SessionID:    sessionID,
		Author: model.AuthorProvenance{
			AgentID:   "claude-architect",
			Harness:   "claude-code",
			Model:     "claude-3-7-sonnet",
			SessionID: sessionID,
		},
		DesiredOutcome:   "In-place refactor of authentication middleware",
		ExpectedArtifact: "internal/auth/middleware.go",
		Scope:            []string{"internal/auth"},
		IdentifiedRisks:  []string{"Session invalidation"},
		IsDestructive:    false,
		Ambiguities:      nil,
		SubmittedAt:      now,
	}

	interp2 := model.Interpretation{
		ID:           "interp-antigravity-1",
		GoalID:       goalID,
		GoalRevision: revision,
		SessionID:    sessionID,
		Author: model.AuthorProvenance{
			AgentID:   "antigravity-integrator",
			Harness:   "antigravity",
			Model:     "gemini-2.5-pro",
			SessionID: sessionID,
		},
		DesiredOutcome:   "In-place refactor of authentication middleware",
		ExpectedArtifact: "internal/auth/middleware.go",
		Scope:            []string{"internal/auth"},
		IdentifiedRisks:  []string{"Token verification latency"},
		IsDestructive:    false,
		Ambiguities:      nil,
		SubmittedAt:      now.Add(1 * time.Second),
	}

	// 1. Save interpretations
	if err := st.SaveInterpretation(ctx, interp1); err != nil {
		t.Fatalf("SaveInterpretation 1 failed: %v", err)
	}
	if err := st.SaveInterpretation(ctx, interp2); err != nil {
		t.Fatalf("SaveInterpretation 2 failed: %v", err)
	}

	// 2. Save InterpretationResolution
	res := model.InterpretationResolution{
		ID:                 "res-interp-001",
		SessionID:          sessionID,
		GoalID:             goalID,
		GoalRevision:       revision,
		State:              model.GoalReady,
		RequiredCount:      2,
		CollectedCount:     2,
		ConsensusConfirmed: true,
		Message:            "Consensus confirmed across independent interpretations",
		ResolvedAt:         now.Add(2 * time.Second),
	}
	if err := st.SaveInterpretationResolution(ctx, res); err != nil {
		t.Fatalf("SaveInterpretationResolution failed: %v", err)
	}

	// 3. Restart store
	if err := st.Close(); err != nil {
		t.Fatalf("failed to close store: %v", err)
	}

	st2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen store failed: %v", err)
	}
	defer st2.Close()

	// 4. Verify interpretations list survived restart
	list, err := st2.ListInterpretations(ctx, goalID, revision)
	if err != nil {
		t.Fatalf("ListInterpretations failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 interpretations, got %d", len(list))
	}
	if list[0].Author.AgentID != "claude-architect" || list[1].Author.AgentID != "antigravity-integrator" {
		t.Errorf("unexpected authors: %v, %v", list[0].Author.AgentID, list[1].Author.AgentID)
	}

	// 5. Verify resolution survived restart
	resRetrieved, err := st2.GetInterpretationResolution(ctx, sessionID, goalID, revision)
	if err != nil {
		t.Fatalf("GetInterpretationResolution failed: %v", err)
	}
	if resRetrieved.State != model.GoalReady {
		t.Errorf("expected state READY, got %v", resRetrieved.State)
	}
	if resRetrieved.RequiredCount != 2 || resRetrieved.CollectedCount != 2 {
		t.Errorf("unexpected counts: required %d, collected %d", resRetrieved.RequiredCount, resRetrieved.CollectedCount)
	}
}
