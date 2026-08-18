package recovery

import (
	"context"
	"testing"
)

func BenchmarkRecover(b *testing.B) {
	mgr := NewManager()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = mgr.Recover(ctx, "t-bm", "cp-bm")
	}
}
