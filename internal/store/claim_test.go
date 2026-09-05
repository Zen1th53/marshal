package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestClaimPersistenceAndRestart(t *testing.T) {
	ctx := context.Background()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "marshal_claim_test.db")

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	claimID := "CLAIM-STORE-001"
	now := time.Now().UTC().Truncate(time.Millisecond)

	claim := model.Claim{
		ID:             claimID,
		GoalID:         "GOAL-100",
		GoalRevision:   1,
		Subject:        "auth.jwt.signature",
		NormalizedText: "JWT signature validated with Ed25519 public key",
		Scope:          "auth.jwt",
		Criticality:    model.CriticalityBlocker,
		State:          model.ClaimStateUnsupported,
		Author: model.AuthorProvenance{
			AgentID:   "codex-1",
			Harness:   "codex-cli",
			Model:     "gpt-5",
			SessionID: "sess-1",
			RunID:     "run-1",
		},
		SupportingEvidence: []model.EvidenceRef{
			{
				EvidenceID:      "EVID-001",
				EvidenceType:    "verification",
				Digest:          "sha256:1111111111111111111111111111111111111111111111111111111111111111",
				Tool:            "go-test",
				IsDeterministic: true,
				CommitSHA:       "abcdef123456",
				CapturedAt:      now,
				Summary:         "pass",
			},
		},
		Binding: model.CodeBinding{
			CommitSHA: "abcdef123456",
			Files:     []string{"internal/auth/jwt.go"},
			Symbols:   []string{"VerifyToken"},
			Tests:     []string{"TestVerifyToken"},
		},
		StateReason: "Initial assertion by codex-1",
		CreatedAt:   now,
		UpdatedAt:   now,
		EvaluatedAt: now,
	}

	if err := st.SaveClaim(ctx, claim); err != nil {
		t.Fatalf("SaveClaim: %v", err)
	}

	// Record transition
	trans := model.ClaimTransition{
		TransitionID: "TRANS-001",
		ClaimID:      claimID,
		FromState:    model.ClaimStateUnsupported,
		ToState:      model.ClaimStateVerified,
		Reason:       "Deterministic test pass on commit abcdef123456",
		Actor: model.AuthorProvenance{
			AgentID: "opencode-verifier",
			Harness: "opencode",
		},
		EvidenceRef: &claim.SupportingEvidence[0],
		Timestamp:   now.Add(time.Second),
	}
	if err := st.RecordClaimTransition(ctx, trans); err != nil {
		t.Fatalf("RecordClaimTransition: %v", err)
	}

	// Update claim state
	claim.State = model.ClaimStateVerified
	claim.StateReason = trans.Reason
	claim.UpdatedAt = trans.Timestamp
	if err := st.SaveClaim(ctx, claim); err != nil {
		t.Fatalf("SaveClaim update: %v", err)
	}

	// Close database
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen database to verify durability
	st2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer st2.Close()
	if err := st2.Migrate(ctx); err != nil {
		t.Fatalf("Migrate 2: %v", err)
	}

	loaded, err := st2.GetClaim(ctx, claimID)
	if err != nil {
		t.Fatalf("GetClaim after restart: %v", err)
	}

	if loaded.ID != claim.ID || loaded.State != model.ClaimStateVerified {
		t.Fatalf("loaded claim mismatch: ID=%s State=%s, want ID=%s State=%s",
			loaded.ID, loaded.State, claim.ID, model.ClaimStateVerified)
	}
	if loaded.Author.AgentID != "codex-1" || loaded.Author.Harness != "codex-cli" {
		t.Fatalf("loaded author mismatch: %+v", loaded.Author)
	}
	if len(loaded.SupportingEvidence) != 1 || loaded.SupportingEvidence[0].EvidenceID != "EVID-001" {
		t.Fatalf("loaded supporting evidence mismatch: %+v", loaded.SupportingEvidence)
	}
	if len(loaded.Binding.Files) != 1 || loaded.Binding.Files[0] != "internal/auth/jwt.go" {
		t.Fatalf("loaded binding mismatch: %+v", loaded.Binding)
	}

	// Verify transitions
	transitions, err := st2.GetClaimTransitions(ctx, claimID)
	if err != nil {
		t.Fatalf("GetClaimTransitions: %v", err)
	}
	if len(transitions) != 1 {
		t.Fatalf("transitions count=%d, want 1", len(transitions))
	}
	if transitions[0].FromState != model.ClaimStateUnsupported || transitions[0].ToState != model.ClaimStateVerified {
		t.Fatalf("transition state mismatch: %s -> %s", transitions[0].FromState, transitions[0].ToState)
	}

	// Test ListClaimsByGoal
	byGoal, err := st2.ListClaimsByGoal(ctx, "GOAL-100", 1)
	if err != nil {
		t.Fatalf("ListClaimsByGoal: %v", err)
	}
	if len(byGoal) != 1 || byGoal[0].ID != claimID {
		t.Fatalf("ListClaimsByGoal count=%d, want 1", len(byGoal))
	}

	// Test ListClaimsByScope
	byScope, err := st2.ListClaimsByScope(ctx, "auth.jwt")
	if err != nil {
		t.Fatalf("ListClaimsByScope: %v", err)
	}
	if len(byScope) != 1 || byScope[0].ID != claimID {
		t.Fatalf("ListClaimsByScope count=%d, want 1", len(byScope))
	}
}
