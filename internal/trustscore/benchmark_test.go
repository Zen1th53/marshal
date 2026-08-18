package trustscore

import (
	"context"
	"testing"
)

func BenchmarkComputeScore(b *testing.B) {
	ev := NewEvaluator()
	ctx := context.Background()
	comps := []Component{{Name: "bm", Score: 90.0}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ev.ComputeScore(ctx, "sha256:bm", comps)
	}
}
