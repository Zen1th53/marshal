package model

import (
	"math"
	"testing"
)

func TestBudgetPatchPreservesOtherLimits(t *testing.T) {
	tokens := int64(100)
	base := BudgetLimit{MaxTotalTokens: &tokens, MaxModelCalls: 10, VerificationReserveCalls: 2}
	changed, err := (BudgetPatch{Dimension: BudgetCost, CostUSD: 1.25}).Apply(&base)
	if err != nil || changed.MaxCostUSD == nil || *changed.MaxCostUSD != 1.25 || changed.MaxModelCalls != 10 || changed.VerificationReserveCalls != 2 || *changed.MaxTotalTokens != 100 {
		t.Fatalf("patch = %+v %v", changed, err)
	}
	cleared, err := (BudgetPatch{Dimension: BudgetTokens, Clear: true}).Apply(changed)
	if err != nil || cleared.MaxTotalTokens != nil || cleared.MaxCostUSD == nil {
		t.Fatalf("clear = %+v %v", cleared, err)
	}
	for _, patch := range []BudgetPatch{{Dimension: BudgetCost, CostUSD: math.NaN()}, {Dimension: BudgetCost, CostUSD: math.Inf(1)}, {Dimension: BudgetCalls, Count: -1}, {Dimension: BudgetTokens, Tokens: -1}, {Dimension: BudgetReserve, Count: 11}, {}} {
		if _, err := patch.Apply(&base); err == nil {
			t.Fatalf("accepted %+v", patch)
		}
	}
}
