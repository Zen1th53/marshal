package tournament

import (
	"context"
	"testing"
)

func BenchmarkEvaluateTournament(b *testing.B) {
	ar := NewArena()
	ctx := context.Background()
	cands := []CandidateRun{{ID: "cand-bm"}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ar.EvaluateTournament(ctx, cands, nil)
	}
}
