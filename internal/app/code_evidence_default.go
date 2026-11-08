//go:build governed || !unix

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// Code evidence is an Extreme feature. Community keeps no state for it and
// these hooks have no effect.

type codeEvidenceHostField = struct{}

func (r *Runtime) wireCodeEvidence(*MarshalService) {}

func withCodeEvidenceTask(ctx context.Context, _ *MarshalService, _ string, _ marshal.Task, _ string) context.Context {
	return ctx
}

func (s *MarshalService) codeEvidenceHandIn(context.Context, string, marshal.Run, marshal.Task, marshal.HandIn) error {
	return nil
}

func (s *MarshalService) codeEvidenceReviewNote(context.Context, string, string) string { return "" }

func (s *MarshalService) codeEvidenceVerify(context.Context, string, marshal.Run, string) {}
