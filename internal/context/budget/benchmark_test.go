package budget

import (
	"context"
	"testing"
)

func BenchmarkAllocate(b *testing.B) {
	mgr := NewManager()
	ctx := context.Background()
	bud := Budget{MaxTokens: 4000, ReserveTokens: 500}
	secs := []SectionPriority{
		{Kind: "sys", Priority: 10, MinTokens: 200, Mandatory: true},
		{Kind: "history", Priority: 5, MinTokens: 500, Mandatory: false},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = mgr.Allocate(ctx, bud, secs)
	}
}
