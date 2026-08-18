package recommendation

import (
	"context"
	"testing"
)

func BenchmarkGenerate(b *testing.B) {
	eng := NewEngine()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = eng.Generate(ctx, "bm query")
	}
}
