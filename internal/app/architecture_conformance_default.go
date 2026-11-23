//go:build governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// The governed edition has no project contract to check against.
func (s *MarshalService) architectureHandInEdition(context.Context, marshal.Task, marshal.HandIn) error {
	return nil
}

func (s *MarshalService) hasMandatoryArchitectureObligation(context.Context, marshal.Run) bool {
	return false
}

func (s *MarshalService) architectureHandInForRunEdition(_ context.Context, run marshal.Run, _ marshal.Task, _ marshal.HandIn) (conformanceOutcome, error) {
	return conformanceOutcome{Policy: run.EffectiveReviewPolicy().Conformance}, nil
}
