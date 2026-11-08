//go:build governed

package app

import (
	"context"
	"encoding/json"
	"time"
)

type requiredMarshalTurnPreparation struct{}
type requiredMarshalTurnCapture struct{}

func (*MarshalCLI) resetRequiredMarshalTurn()                 {}
func (*MarshalCLI) prepareRequiredMarshalBinary(string) error { return nil }
func (*MarshalCLI) validateRequiredMarshalBinary() error      { return nil }
func (*MarshalCLI) decodeRequiredMarshalResult(payload []byte, out any) error {
	return json.Unmarshal(payload, out)
}
func (*MarshalCLI) captureRequiredMarshalTurn(string, string, []byte, []byte, string, string, time.Time, time.Time) {
}

func (*MarshalCLI) prepareRequiredMarshalModelTurn(ctx context.Context, prompt string) (context.Context, string, func() error, error) {
	return ctx, prompt, func() error { return nil }, nil
}

func (*MarshalCLI) validateRequiredMarshalModelEnvelope(string, string) error { return nil }

// marshalKnowledgeViaRecall: Community has no required-context envelope, so
// observed project knowledge always stays inline.
const marshalKnowledgeViaRecall = false

func (*MarshalCLI) marshalKnowledgeFits(string, string) bool { return true }

// providerLimitFallback: Community keeps its role provider choice unchanged.
const providerLimitFallback = false
