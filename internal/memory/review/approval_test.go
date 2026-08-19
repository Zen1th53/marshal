package review_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/review"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT91ProtectedMemoryReviewWorkflow(t *testing.T) {
	mgr := review.NewManager()
	ctx := context.Background()
	now := time.Now().UTC()

	candidate := model.MemoryRecordV2{
		ID:          "MEM-REV-01",
		ProjectID:   "PROJ-1",
		Kind:        model.MemoryKindDecision,
		Lifecycle:   model.MemoryCandidate,
		Authority:   model.AuthorityAgent,
		Title:       "Database Migration Decision",
		Body:        "Use PostgreSQL with Row Level Security",
		Scope:       string(model.ScopeProject),
		ScopeID:     "PROJ-1",
		ObservedAt:  now,
		ValidFrom:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
		EvidenceIDs: []string{"EVID-REV-1"},
	}

	// 1. Create Review Request
	req, err := mgr.CreateReviewRequest(ctx, candidate, "agent-dev-1")
	if err != nil {
		t.Fatalf("CreateReviewRequest: %v", err)
	}
	if req.CandidateDigest != candidate.CanonicalDigest() {
		t.Fatalf("expected request digest to match candidate canonical digest")
	}

	// 2. Unauthorized role attempting approval must fail
	_, err = mgr.Approve(ctx, req.RequestID, candidate, "dev-junior", "developer")
	if !errors.Is(err, review.ErrUnauthorizedApprover) {
		t.Fatalf("expected ErrUnauthorizedApprover, got: %v", err)
	}

	// 3. Candidate mutated after review creation must fail approval (digest mismatch)
	mutated := candidate
	mutated.Body = "Use SQLite without RLS instead" // altered payload
	_, err = mgr.Approve(ctx, req.RequestID, mutated, "admin-lead", "operator")
	if !errors.Is(err, review.ErrDigestMismatch) {
		t.Fatalf("expected ErrDigestMismatch for mutated candidate, got: %v", err)
	}

	// 4. Authorized operator approving unmodified candidate succeeds
	approved, err := mgr.Approve(ctx, req.RequestID, candidate, "admin-lead", "operator")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.Lifecycle != model.MemoryDurable {
		t.Fatalf("expected lifecycle Durable after operator approval, got %s", approved.Lifecycle)
	}
	if approved.Authority != model.AuthorityOperator {
		t.Fatalf("expected authority Operator, got %s", approved.Authority)
	}

	// 5. Replay approval on already resolved request fails
	_, err = mgr.Approve(ctx, req.RequestID, candidate, "admin-lead", "operator")
	if !errors.Is(err, review.ErrAlreadyResolved) {
		t.Fatalf("expected ErrAlreadyResolved for approval replay, got: %v", err)
	}
}
