package adapter

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/attestation"
)

type WorkerAttestationService struct {
	verifier *attestation.Verifier
}

func NewWorkerAttestationService(verifier *attestation.Verifier) *WorkerAttestationService {
	return &WorkerAttestationService{verifier: verifier}
}

func (s *WorkerAttestationService) ValidateNode(ctx context.Context, report attestation.Report, challenge string) (*attestation.Verdict, error) {
	if s == nil || s.verifier == nil {
		return nil, fmt.Errorf("attestation service uninitialized")
	}
	return s.verifier.Verify(ctx, report, challenge)
}
