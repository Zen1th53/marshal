//go:build governed

package app

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/verification"
)

func (s *VerificationService) completeRequiredContextAttestation(ctx context.Context, session verification.Session, envelope verification.BundleEnvelope, provenance string) (verification.CompletionAttestation, error) {
	a, err := verification.NewCompletionAttestation("completion-"+session.ID+fmt.Sprintf("-%d", session.Version), session, envelope.Bundle.ManifestDigest, provenance, s.now())
	if err != nil {
		return verification.CompletionAttestation{}, err
	}
	if err = s.runtime.store.AppendCompletionAttestation(ctx, a); err != nil {
		return verification.CompletionAttestation{}, err
	}
	return a, nil
}
