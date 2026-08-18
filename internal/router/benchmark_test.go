package router

import (
	"context"
	"testing"
)

func BenchmarkRoute(b *testing.B) {
	rt := NewRouter()
	ctx := context.Background()
	caps := []string{"code", "refactor"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = rt.Route(ctx, caps, 32000)
	}
}
