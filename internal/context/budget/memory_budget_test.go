package budget_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/context/budget"
)

func TestT115MemoryContextBudgetPolicy(t *testing.T) {
	mgr := budget.NewManager()
	ctx := context.Background()

	b := budget.Budget{
		MaxTokens:     1000,
		ReserveTokens: 200, // Available: 800 tokens
	}

	// 1. Mandatory policy overflow fails hard
	overflowSections := []budget.SectionPriority{
		{Kind: "safety_policy", Priority: 100, MinTokens: 900, Mandatory: true},
	}
	_, err := mgr.Allocate(ctx, b, overflowSections)
	if !errors.Is(err, budget.ErrMandatoryOverflow) {
		t.Fatalf("expected ErrMandatoryOverflow, got: %v", err)
	}

	// 2. Prioritized allocation: Mandatory policy + Pinned Block + Retrieved memories
	validSections := []budget.SectionPriority{
		{Kind: "safety_policy", Priority: 100, MinTokens: 200, Mandatory: true},
		{Kind: "pinned_block", Priority: 90, MinTokens: 300, Mandatory: false},
		{Kind: "retrieved_decision", Priority: 80, MinTokens: 200, Mandatory: false},
		{Kind: "low_value_transcript_1", Priority: 20, MinTokens: 200, Mandatory: false},
		{Kind: "low_value_transcript_2", Priority: 10, MinTokens: 200, Mandatory: false},
	}

	dec, err := mgr.Allocate(ctx, b, validSections)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	// 200 + 300 + 200 = 700 <= 800 available
	// low_value_transcript_1 and low_value_transcript_2 should be dropped!
	if len(dec.Dropped) < 2 {
		t.Fatalf("expected at least 2 dropped lower-value sections, got: %+v", dec.Dropped)
	}
	if dec.EstimatedTokens > 800 {
		t.Fatalf("total allocated tokens %d exceeded available limit 800", dec.EstimatedTokens)
	}
}
