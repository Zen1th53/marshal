package budget

import (
	"context"
	"testing"
)

func TestManagerAllocate(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	dec, err := mgr.Allocate(ctx,
		Budget{MaxTokens: 1000, ReserveTokens: 100},
		[]SectionPriority{
			{Kind: "system", Priority: 100, MinTokens: 200, Mandatory: true},
			{Kind: "memory", Priority: 50, MinTokens: 300, Mandatory: false},
		},
	)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if dec.Action != "ALLOCATE_OK" {
		t.Fatalf("expected ALLOCATE_OK, got %s", dec.Action)
	}
}
