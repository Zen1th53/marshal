package app

import (
	"context"
	"errors"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
)

type conformanceOutcome struct {
	Policy    marshal.ConformancePolicy
	Findings  []string
	Omitted   bool
	Mandatory bool
}

// EffectiveConformancePolicy returns the conformance policy in force for run
// and reports whether a mandatory architecture binding locks it to enforce.
func (s *MarshalService) EffectiveConformancePolicy(ctx context.Context, run marshal.Run) (marshal.ConformancePolicy, bool) {
	if s.hasMandatoryArchitectureObligation(ctx, run) {
		return marshal.ConformanceEnforce, true
	}
	return run.EffectiveReviewPolicy().Conformance, false
}

// architectureHandIn returns a hand-in whose code disagrees with the
// accepted architecture (see architectureHandInEdition).
func (s *MarshalService) architectureHandIn(ctx context.Context, t marshal.Task, h marshal.HandIn) error {
	if s == nil || s.runtime == nil || s.runtime.store == nil {
		return nil
	}
	return s.architectureHandInEdition(ctx, t, h)
}

func (s *MarshalService) architectureHandInForRun(ctx context.Context, run marshal.Run, t marshal.Task, h marshal.HandIn) (conformanceOutcome, error) {
	if s == nil || s.runtime == nil || s.runtime.store == nil {
		return conformanceOutcome{Policy: run.EffectiveReviewPolicy().Conformance}, nil
	}
	return s.architectureHandInForRunEdition(ctx, run, t, h)
}

// checkArchitectureReview refuses incomplete reviews and converts mismatches to returns.
func checkArchitectureReview(r *marshal.Review, required bool) error {
	if !required {
		return nil
	}
	if len(r.ArchitectureMismatches) > 0 {
		for _, mismatch := range r.ArchitectureMismatches {
			if !strings.Contains(mismatch, ":") {
				return errors.New("architecture review incomplete: mismatches require file:part")
			}
		}
		r.ArchitectureVerdict = "mismatches"
		r.Verdict = marshal.VerdictReturn
		r.Reasons = append(r.Reasons, r.ArchitectureMismatches...)
		return nil
	}
	if r.ArchitectureVerdict != "conforms" {
		return errors.New("architecture review incomplete: state conforms or list ArchitectureMismatches with file:part")
	}
	return nil
}
