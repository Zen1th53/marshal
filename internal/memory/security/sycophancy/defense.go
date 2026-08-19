package sycophancy

import (
	"context"
	"errors"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrUnsafeScopeBroadening        = errors.New("unsafe write: untrusted agent cannot write project-level policy or decisions")
	ErrUnverifiedProcedurePromotion = errors.New("unsafe write: procedure requires empirical test verification evidence before durable promotion")
)

type OriginType string

const (
	OriginUserPrompt  OriginType = "USER_PROMPT"
	OriginAgentOutput OriginType = "AGENT_OUTPUT"
	OriginOperator    OriginType = "OPERATOR_ADMIN"
)

type WriteContext struct {
	RepetitionCount int        `json:"repetition_count"`
	Origin          OriginType `json:"origin"`
	HasTestEvidence bool       `json:"has_test_evidence"`
}

type Guard struct{}

func NewGuard() *Guard {
	return &Guard{}
}

// EvaluateWrite inspects candidate writes to prevent conversational sycophancy or unauthorized policy escalation.
func (g *Guard) EvaluateWrite(ctx context.Context, rec model.MemoryRecordV2, wCtx WriteContext) (model.MemoryRecordV2, error) {
	// Rule 1: Scope Broadening Check
	if (rec.Scope == string(model.ScopeProject) || rec.Kind == model.MemoryKindDecision) && rec.Authority != model.AuthorityOperator && rec.Authority != model.AuthorityPolicy {
		return model.MemoryRecordV2{}, ErrUnsafeScopeBroadening
	}

	// Rule 2: Procedure Verification Check
	if rec.Kind == model.MemoryKindProcedural && rec.Lifecycle == model.MemoryDurable && !wCtx.HasTestEvidence && wCtx.Origin != OriginOperator {
		return model.MemoryRecordV2{}, ErrUnverifiedProcedurePromotion
	}

	// Rule 3: Conversational repetition clamps to candidate/unverified
	if wCtx.Origin == OriginUserPrompt || wCtx.Origin == OriginAgentOutput {
		if rec.Authority == model.AuthorityVerified {
			rec.Authority = model.AuthorityAgent
		}
		if rec.Lifecycle == model.MemoryDurable {
			rec.Lifecycle = model.MemoryCandidate
		}
	}

	return rec, nil
}
