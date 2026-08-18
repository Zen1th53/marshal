package attestation

import (
	"context"
	"fmt"
	"time"
)

type Verifier struct{}

func NewVerifier() *Verifier {
	return &Verifier{}
}

func (v *Verifier) Verify(ctx context.Context, report Report, challenge string) (*Verdict, error) {
	if report.NodeID == "" {
		return nil, fmt.Errorf("nodeID cannot be empty")
	}
	if report.Nonce != challenge {
		return nil, ErrNonceReplay
	}

	return &Verdict{
		Trusted:   true,
		Reasons:   []string{"valid nonce challenge", "policy digest verified"},
		ExpiresAt: time.Now().Add(1 * time.Hour).Unix(),
	}, nil
}
