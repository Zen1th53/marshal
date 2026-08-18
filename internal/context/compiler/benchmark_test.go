package compiler

import (
	"context"
	"testing"
)

func BenchmarkCompilerCompile(b *testing.B) {
	comp := NewCompiler()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := string(rune(i))
		_, _ = comp.Compile(ctx, id, "t-bm", "a-bm", "sample benchmark prompt", 1000, nil, nil)
	}
}
