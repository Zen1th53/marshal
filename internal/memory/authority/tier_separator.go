package authority

import (
	"context"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

type PrecedenceResult struct {
	WinningRecord    model.MemoryRecordV2 `json:"winning_record"`
	SuppressedRecord model.MemoryRecordV2 `json:"suppressed_record"`
	Reason           string               `json:"reason"`
}

type TierResolver struct{}

func NewTierResolver() *TierResolver {
	return &TierResolver{}
}

func getAuthorityRank(auth model.MemoryAuthority) int {
	switch auth {
	case model.AuthorityPolicy, model.AuthorityOperator:
		return 100 // Top tier: Immutable policy & Operator rules
	case model.AuthorityVerified:
		return 80 // Objective verified facts
	case model.AuthorityAgent:
		return 40 // Subjective / agent beliefs
	default:
		return 20 // Unverified
	}
}

// ResolvePrecedence arbitrates conflict between two memory records based strictly on deterministic authority tiering.
func (r *TierResolver) ResolvePrecedence(ctx context.Context, a, b model.MemoryRecordV2) (PrecedenceResult, error) {
	rankA := getAuthorityRank(a.Authority)
	rankB := getAuthorityRank(b.Authority)

	if rankA >= rankB {
		return PrecedenceResult{
			WinningRecord:    a,
			SuppressedRecord: b,
			Reason:           "authority tier dominance: higher tier overrides lower tier",
		}, nil
	}

	return PrecedenceResult{
		WinningRecord:    b,
		SuppressedRecord: a,
		Reason:           "authority tier dominance: higher tier overrides lower tier",
	}, nil
}

// CanApplyPersonalization checks whether a user preference conflicts with active security policies or facts.
func (r *TierResolver) CanApplyPersonalization(pref model.MemoryRecordV2, activeConstraints []model.MemoryRecordV2) bool {
	prefLower := strings.ToLower(pref.Title + " " + pref.Body)

	for _, c := range activeConstraints {
		if c.Authority == model.AuthorityPolicy || c.Authority == model.AuthorityOperator {
			// Check if preference attempts to weaken security policy
			if strings.Contains(prefLower, "http") && strings.Contains(strings.ToLower(c.Body), "tls") {
				return false
			}
			if strings.Contains(prefLower, "disable auth") {
				return false
			}
		}
	}

	return true
}
