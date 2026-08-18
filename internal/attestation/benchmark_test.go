package attestation

import (
	"context"
	"testing"
)

func BenchmarkVerify(b *testing.B) {
	ver := NewVerifier()
	ctx := context.Background()
	report := Report{NodeID: "n-bm", Nonce: "nonce-bm"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ver.Verify(ctx, report, "nonce-bm")
	}
}
