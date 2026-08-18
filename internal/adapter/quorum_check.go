package adapter

import (
	"context"

	"github.com/Zen1th53/marshal/internal/gate"
	"github.com/Zen1th53/marshal/internal/verify/quorum"
)

type QuorumCheckSource interface {
	Resolve(context.Context, gate.CheckRequest) ([]quorum.Requirement, []quorum.Attestation, quorum.Provenance, error)
}

func NewQuorumCheck(engine *quorum.Engine, source QuorumCheckSource) gate.CheckFunc {
	return func(ctx context.Context, request gate.CheckRequest) (gate.CheckResult, error) {
		if engine == nil || source == nil {
			return gate.CheckResult{Status: gate.CheckStatusBlocked}, quorum.ErrAuthorityUnavailable
		}
		requirements, attestations, provenance, err := source.Resolve(ctx, request)
		if err != nil {
			return gate.CheckResult{Status: gate.CheckStatusBlocked}, err
		}
		evaluation, err := engine.Evaluate(ctx, requirements, attestations, provenance)
		result := gate.CheckResult{Status: gate.CheckStatusBlocked}
		if len(evaluation.Accepted) > 0 {
			result.EvidenceID = evaluation.Accepted[0].EvidenceID
			result.VerifierID = evaluation.Accepted[0].Subject
		}
		if err != nil {
			return result, err
		}
		if !evaluation.Satisfied {
			return result, gate.ErrQuorumUnmet
		}
		result.Status = gate.CheckStatusPass
		result.Reason = gate.CodeAllowed
		return result, nil
	}
}
