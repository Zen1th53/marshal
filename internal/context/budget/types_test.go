package budget

import "testing"

func TestBudgetTypes(t *testing.T) {
	b := Budget{MaxTokens: 8000, ReserveTokens: 1000}
	if b.MaxTokens != 8000 {
		t.Fatalf("expected 8000, got %d", b.MaxTokens)
	}
}
