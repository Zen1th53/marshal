package trust

import (
	"fmt"
	"math"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type ScoreBreakdown struct {
	AuthorityScore   float64  `json:"authority_score"`
	EvidenceScore    float64  `json:"evidence_score"`
	FreshnessScore   float64  `json:"freshness_score"`
	LifecyclePenalty float64  `json:"lifecycle_penalty"`
	FinalScore       float64  `json:"final_score"`
	Reasons          []string `json:"reasons"`
}

type Scorer struct{}

func NewScorer() *Scorer {
	return &Scorer{}
}

// Score computes an explainable, bounded retrieval feature score for a memory record.
func (s *Scorer) Score(rec model.MemoryRecordV2, asOf time.Time) ScoreBreakdown {
	var reasons []string

	// 1. Authority score
	var authScore float64
	switch rec.Authority {
	case model.AuthorityOperator:
		authScore = 1.0
		reasons = append(reasons, "authority:operator (1.0)")
	case model.AuthorityPolicy:
		authScore = 0.90
		reasons = append(reasons, "authority:policy (0.90)")
	case model.AuthorityVerified:
		authScore = 0.80
		reasons = append(reasons, "authority:verified (0.80)")
	case model.AuthorityAgent:
		authScore = 0.40
		reasons = append(reasons, "authority:agent (0.40)")
	default:
		authScore = 0.20
		reasons = append(reasons, "authority:unspecified (0.20)")
	}

	// 2. Evidence score
	var evidScore float64
	nEvid := len(rec.EvidenceIDs)
	switch {
	case nEvid >= 2:
		evidScore = 1.0
		reasons = append(reasons, fmt.Sprintf("evidence:multi-source (%d IDs)", nEvid))
	case nEvid == 1:
		evidScore = 0.75
		reasons = append(reasons, "evidence:single-source (1 ID)")
	default:
		evidScore = 0.20
		reasons = append(reasons, "evidence:none (0 IDs)")
	}

	// 3. Freshness score (30-day half-life decay)
	ageHours := asOf.Sub(rec.ObservedAt).Hours()
	if ageHours < 0 {
		ageHours = 0
	}
	freshnessScore := math.Exp(-ageHours / (30 * 24))
	reasons = append(reasons, fmt.Sprintf("freshness:age=%.1fh, decay=%.3f", ageHours, freshnessScore))

	// 4. Lifecycle penalty
	var penalty float64
	switch rec.Lifecycle {
	case model.MemoryDurable:
		penalty = 0.0
	case model.MemoryVerified:
		penalty = 0.05
		reasons = append(reasons, "lifecycle:verified (penalty -0.05)")
	case model.MemoryCandidate:
		penalty = 0.20
		reasons = append(reasons, "lifecycle:candidate (penalty -0.20)")
	case model.MemoryStale:
		penalty = 0.40
		reasons = append(reasons, "lifecycle:stale (penalty -0.40)")
	case model.MemoryConflicted:
		penalty = 0.50
		reasons = append(reasons, "lifecycle:conflicted (penalty -0.50)")
	case model.MemorySuperseded, model.MemoryTombstoned, model.MemoryRejected:
		penalty = 0.90
		reasons = append(reasons, fmt.Sprintf("lifecycle:%s (penalty -0.90)", rec.Lifecycle))
	default:
		penalty = 0.20
	}

	raw := (0.40 * authScore) + (0.35 * evidScore) + (0.25 * freshnessScore) - penalty
	if raw < 0.0 {
		raw = 0.0
	} else if raw > 1.0 {
		raw = 1.0
	}

	return ScoreBreakdown{
		AuthorityScore:   authScore,
		EvidenceScore:    evidScore,
		FreshnessScore:   freshnessScore,
		LifecyclePenalty: penalty,
		FinalScore:       raw,
		Reasons:          reasons,
	}
}
