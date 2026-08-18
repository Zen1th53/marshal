package evolution

import (
	"context"
	"testing"
)

func BenchmarkStart(b *testing.B) {
	lab := NewLab()
	ctx := context.Background()
	cfg := LabConfig{Population: 5, Generations: 2}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lab.Start(ctx, cfg)
	}
}
