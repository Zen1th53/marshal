package adapter

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/attestation"
)

func TestWorkerAttestationServiceAdapter(t *testing.T) {
	ver := attestation.NewVerifier()
	ctx := context.Background()
	svc := NewWorkerAttestationService(ver)

	verdict, err := svc.ValidateNode(ctx, attestation.Report{NodeID: "n-1", Nonce: "ch-1"}, "ch-1")
	if err != nil {
		t.Fatalf("ValidateNode failed: %v", err)
	}
	if !verdict.Trusted {
		t.Fatalf("expected Trusted = true")
	}
}
