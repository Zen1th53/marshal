package attestation

import (
	"context"
	"testing"
)

func TestVerifierVerify(t *testing.T) {
	ver := NewVerifier()
	ctx := context.Background()

	report := Report{NodeID: "node-1", Nonce: "ch-123"}
	verdict, err := ver.Verify(ctx, report, "ch-123")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !verdict.Trusted {
		t.Fatalf("expected Trusted = true")
	}
}
