package provenance

import (
	"context"
	"testing"
)

func BenchmarkEngineTrace(b *testing.B) {
	eng := NewEngine()
	ctx := context.Background()
	_, _ = eng.Begin(ctx, "chg-bm", "task-bm", "agent-bm", "codex", "ctx", "patch")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = eng.Trace(ctx, "chg-bm")
	}
}
