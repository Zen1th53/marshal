package decision

import (
	"context"
	"testing"
)

func BenchmarkDecisionPropose(b *testing.B) {
	eng := NewEngine()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := string(rune(i))
		_, _ = eng.Propose(ctx, id, "t-bm", "a-bm", "ADR", "ctx", "dec")
	}
}
