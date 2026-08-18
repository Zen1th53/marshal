package adapter

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/context/budget"
)

func TestBudgetGovernorAdapter(t *testing.T) {
	mgr := budget.NewManager()
	ctx := context.Background()
	gov := NewBudgetGovernor(mgr)

	dec, err := gov.ManageBudget(ctx, 2000, []budget.SectionPriority{{Kind: "sys", MinTokens: 100, Mandatory: true}})
	if err != nil {
		t.Fatalf("ManageBudget failed: %v", err)
	}
	if dec.Action != "ALLOCATE_OK" {
		t.Fatalf("expected ALLOCATE_OK, got %s", dec.Action)
	}
}
