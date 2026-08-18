package attestation

import (
	"context"
	"errors"
	"testing"
)

func TestT35A04AdversarialBoundaries(t *testing.T) {
	ver := NewVerifier()
	ctx := context.Background()

	// Nonce replay mismatch
	report := Report{NodeID: "node-1", Nonce: "bad-nonce"}
	_, err := ver.Verify(ctx, report, "valid-nonce")
	if !errors.Is(err, ErrNonceReplay) {
		t.Fatalf("expected ErrNonceReplay, got %v", err)
	}
}
