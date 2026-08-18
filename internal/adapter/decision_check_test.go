package adapter

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/decision"
)

func TestDecisionAdapter(t *testing.T) {
	eng := decision.NewEngine()
	ctx := context.Background()
	ad := NewDecisionAdapter(eng)

	rec, err := ad.SubmitADR(ctx, "d-1", "t-1", "a-1", "ADR", "ctx", "dec")
	if err != nil {
		t.Fatalf("SubmitADR failed: %v", err)
	}
	if rec.ID != "d-1" {
		t.Fatalf("expected d-1, got %s", rec.ID)
	}
}
