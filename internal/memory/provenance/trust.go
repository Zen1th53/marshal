package provenance

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrMissingEvidence = errors.New("protected memory promotion requires verifiable evidence IDs")
)

// TrustEvaluator computes deterministic derived trust scores and enforces evidence binding.
type TrustEvaluator struct{}

func NewTrustEvaluator() *TrustEvaluator {
	return &TrustEvaluator{}
}

// ValidateProtectedPromotion ensures protected memory kinds (decision, finding, failure)
// have evidence IDs attached before promotion.
func (e *TrustEvaluator) ValidateProtectedPromotion(rec model.MemoryRecordV2) error {
	if rec.Kind == model.MemoryKindDecision || rec.Kind == model.MemoryKindFinding || rec.Kind == model.MemoryKindFailure {
		if len(rec.EvidenceIDs) == 0 {
			return fmt.Errorf("%w: record %s (%s) has no evidence IDs", ErrMissingEvidence, rec.ID, rec.Kind)
		}
	}
	return nil
}

// EvaluateTrust derives a normalized [0.0, 1.0] trust score based on:
// 1. Source Authority (0.40 weight)
// 2. Evidence presence and quantity (0.35 weight)
// 3. Temporal Freshness decay (0.25 weight)
func (e *TrustEvaluator) EvaluateTrust(rec model.MemoryRecordV2, asOf time.Time) float64 {
	// 1. Authority base score
	var authorityScore float64
	switch rec.Authority {
	case model.AuthorityOperator:
		authorityScore = 1.0
	case model.AuthorityPolicy:
		authorityScore = 0.90
	case model.AuthorityVerified:
		authorityScore = 0.80
	case model.AuthorityAgent:
		authorityScore = 0.40
	default:
		authorityScore = 0.20
	}

	// 2. Evidence factor
	var evidenceScore float64
	switch {
	case len(rec.EvidenceIDs) >= 2:
		evidenceScore = 1.0
	case len(rec.EvidenceIDs) == 1:
		evidenceScore = 0.75
	default:
		evidenceScore = 0.20
	}

	// 3. Temporal freshness decay (half-life of 30 days)
	ageHours := asOf.Sub(rec.ObservedAt).Hours()
	if ageHours < 0 {
		ageHours = 0
	}
	freshnessScore := math.Exp(-ageHours / (30 * 24)) // Exponential decay

	// Weighted total
	total := (0.40 * authorityScore) + (0.35 * evidenceScore) + (0.25 * freshnessScore)

	if total > 1.0 {
		total = 1.0
	} else if total < 0.0 {
		total = 0.0
	}
	return total
}
