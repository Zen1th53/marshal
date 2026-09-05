package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestCollaborationStoreAndRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "marshal_collab_test.db")

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	sessionID := "sess-collab-100"
	goalID := "goal-collab-1"
	var revision int64 = 1

	now := time.Now().UTC().Truncate(time.Millisecond)

	participants := []model.Participant{
		{
			AgentID:  "claude-arch",
			Role:     model.RoleArchitect,
			Harness:  "claude-code",
			Model:    "claude-3-7-sonnet",
			IsActive: true,
		},
		{
			AgentID:  "codex-dev",
			Role:     model.RoleDeveloper,
			Harness:  "codex",
			Model:    "gpt-4o",
			IsActive: true,
		},
		{
			AgentID:  "opencode-qa",
			Role:     model.RoleQA,
			Harness:  "opencode",
			Model:    "deepseek-coder",
			IsActive: true,
		},
		{
			AgentID:  "antigravity-int",
			Role:     model.RoleDeveloper,
			Harness:  "antigravity",
			Model:    "gemini-2.5-pro",
			IsActive: true,
		},
	}

	sess := model.TeamSession{
		SessionID:    sessionID,
		GoalID:       goalID,
		GoalRevision: revision,
		Participants: participants,
		ActiveTurn:   "claude-arch",
		TurnSequence: 1,
		Status:       "ACTIVE",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// 1. Save team session
	if err := st.SaveTeamSession(ctx, sess); err != nil {
		t.Fatalf("SaveTeamSession failed: %v", err)
	}

	// 2. Save agent messages
	msg1 := model.AgentMessage{
		ID:        "msg-collab-01",
		SessionID: sessionID,
		TaskID:    "task-collab-1",
		From: model.AuthorProvenance{
			AgentID:   "claude-arch",
			Harness:   "claude-code",
			Model:     "claude-3-7-sonnet",
			SessionID: sessionID,
		},
		Kind:      model.MessageFinding,
		Content:   "Architecture plan drafted for distributed cache",
		CreatedAt: now.Add(1 * time.Second),
	}
	if err := st.SaveAgentMessage(ctx, msg1); err != nil {
		t.Fatalf("SaveAgentMessage 1 failed: %v", err)
	}

	msg2 := model.AgentMessage{
		ID:        "msg-collab-02",
		SessionID: sessionID,
		TaskID:    "task-collab-1",
		From: model.AuthorProvenance{
			AgentID:   "codex-dev",
			Harness:   "codex",
			Model:     "gpt-4o",
			SessionID: sessionID,
		},
		To:          "opencode-qa",
		Kind:        model.MessageHandoffProposal,
		Content:     "Cache implementation ready for QA validation",
		ClaimIDs:    []string{"claim-cache-pass"},
		EvidenceIDs: []string{"ev-test-pass"},
		CreatedAt:   now.Add(2 * time.Second),
	}
	if err := st.SaveAgentMessage(ctx, msg2); err != nil {
		t.Fatalf("SaveAgentMessage 2 failed: %v", err)
	}

	// 3. Restart store (simulate process crash / restart)
	if err := st.Close(); err != nil {
		t.Fatalf("failed to close store: %v", err)
	}

	st2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen store failed: %v", err)
	}
	defer st2.Close()

	// 4. Verify session and messages survived restart
	sessRetrieved, err := st2.GetTeamSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("GetTeamSession failed: %v", err)
	}
	if len(sessRetrieved.Participants) != 4 {
		t.Fatalf("expected 4 participants, got %d", len(sessRetrieved.Participants))
	}
	if sessRetrieved.ActiveTurn != "claude-arch" {
		t.Errorf("expected active turn claude-arch, got %s", sessRetrieved.ActiveTurn)
	}

	msgs, err := st2.ListAgentMessages(ctx, sessionID, 10)
	if err != nil {
		t.Fatalf("ListAgentMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[1].Kind != model.MessageHandoffProposal || msgs[1].To != "opencode-qa" {
		t.Errorf("unexpected msg2 data: %+v", msgs[1])
	}
}
