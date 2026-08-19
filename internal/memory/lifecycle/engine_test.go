package lifecycle_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/lifecycle"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT82LifecycleTransitionMatrix(t *testing.T) {
	sm := lifecycle.NewStateMachine()

	ctx := context.Background()

	// 1. Legal forward progression: Observed -> Candidate
	rec := model.MemoryRecordV2{
		ID:        "MEM-LC-01",
		Kind:      model.MemoryKindSemantic,
		Lifecycle: model.MemoryObserved,
		Authority: model.AuthorityAgent,
	}

	res, err := sm.Transition(ctx, rec, model.MemoryCandidate, model.AuthorityAgent)
	if err != nil {
		t.Fatalf("Transition to Candidate: %v", err)
	}
	if res.Lifecycle != model.MemoryCandidate {
		t.Fatalf("expected lifecycle candidate, got: %s", res.Lifecycle)
	}

	// 2. Candidate -> Verified requires AuthorityVerified or higher
	// Single agent cannot directly verify
	_, err = sm.Transition(ctx, res, model.MemoryVerified, model.AuthorityAgent)
	if !errors.Is(err, lifecycle.ErrInsufficientAuthority) {
		t.Fatalf("expected ErrInsufficientAuthority for single agent verification, got: %v", err)
	}

	// Verified with AuthorityVerified succeeds
	verified, err := sm.Transition(ctx, res, model.MemoryVerified, model.AuthorityVerified)
	if err != nil {
		t.Fatalf("Transition to Verified: %v", err)
	}
	if verified.Lifecycle != model.MemoryVerified {
		t.Fatalf("expected lifecycle verified, got: %s", verified.Lifecycle)
	}

	// 3. Verified -> Durable succeeds with policy/operator/verified authority
	durable, err := sm.Transition(ctx, verified, model.MemoryDurable, model.AuthorityPolicy)
	if err != nil {
		t.Fatalf("Transition to Durable: %v", err)
	}
	if durable.Lifecycle != model.MemoryDurable {
		t.Fatalf("expected lifecycle durable, got: %s", durable.Lifecycle)
	}

	// 4. Stale -> Durable directly is forbidden (must re-verify first)
	stale := durable
	stale.Lifecycle = model.MemoryStale
	_, err = sm.Transition(ctx, stale, model.MemoryDurable, model.AuthorityPolicy)
	if !errors.Is(err, lifecycle.ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition for Stale->Durable direct bypass, got: %v", err)
	}

	// Stale -> Candidate or Verified is allowed for re-verification
	reCandidate, err := sm.Transition(ctx, stale, model.MemoryCandidate, model.AuthorityAgent)
	if err != nil {
		t.Fatalf("Stale -> Candidate failed: %v", err)
	}
	if reCandidate.Lifecycle != model.MemoryCandidate {
		t.Fatalf("expected candidate, got %s", reCandidate.Lifecycle)
	}

	// 5. Tombstoned record cannot transition to Candidate/Verified/Durable
	tombstoned := durable
	tombstoned.Lifecycle = model.MemoryTombstoned
	_, err = sm.Transition(ctx, tombstoned, model.MemoryCandidate, model.AuthorityOperator)
	if !errors.Is(err, lifecycle.ErrTerminalLifecycle) {
		t.Fatalf("expected ErrTerminalLifecycle for Tombstoned record, got: %v", err)
	}
}

func TestT82ForbiddenAgentSelfPromotionForDecisions(t *testing.T) {
	sm := lifecycle.NewStateMachine()
	ctx := context.Background()

	// Decision or Security Finding kind requires AuthorityOperator or AuthorityPolicy to reach Durable
	rec := model.MemoryRecordV2{
		ID:        "MEM-DEC-01",
		Kind:      model.MemoryKindDecision,
		Lifecycle: model.MemoryVerified,
		Authority: model.AuthorityAgent,
	}

	// Agent trying to promote decision to Durable must fail
	_, err := sm.Transition(ctx, rec, model.MemoryDurable, model.AuthorityAgent)
	if !errors.Is(err, lifecycle.ErrInsufficientAuthority) {
		t.Fatalf("expected ErrInsufficientAuthority for agent promoting Decision, got: %v", err)
	}

	// Operator promoting decision succeeds
	durable, err := sm.Transition(ctx, rec, model.MemoryDurable, model.AuthorityOperator)
	if err != nil {
		t.Fatalf("Operator promotion failed: %v", err)
	}
	if durable.Lifecycle != model.MemoryDurable {
		t.Fatalf("expected durable, got %s", durable.Lifecycle)
	}
}
