package fusion

import (
	"fmt"
	"sort"

	"github.com/Zen1th53/marshal/internal/model"
)

type ChannelMatch struct {
	MemoryID string  `json:"memory_id"`
	Rank     int     `json:"rank"`
	RawScore float64 `json:"raw_score"`
}

type ScoreBreakdown struct {
	RRFScore         float64  `json:"rrf_score"`
	ExactMatchBoost  float64  `json:"exact_match_boost"`
	AuthorityBoost   float64  `json:"authority_boost"`
	LifecyclePenalty float64  `json:"lifecycle_penalty"`
	FinalRankScore   float64  `json:"final_rank_score"`
	Reasons          []string `json:"reasons"`
}

type FusedResult struct {
	MemoryID  string         `json:"memory_id"`
	RankScore float64        `json:"rank_score"`
	Breakdown ScoreBreakdown `json:"breakdown"`
}

type Config struct {
	K float64 // RRF constant, default 60.0
}

type Fuser struct {
	config Config
}

func NewFuser(config Config) *Fuser {
	if config.K <= 0 {
		config.K = 60.0
	}
	return &Fuser{config: config}
}

// Fuse combines multi-channel candidate lists with RRF and bounded explainable feature adjustments.
func (f *Fuser) Fuse(channels [][]ChannelMatch, records map[string]model.MemoryRecordV2, exactMatchIDs []string, limit int) []FusedResult {
	rrfScores := make(map[string]float64)

	exactMap := make(map[string]bool)
	for _, id := range exactMatchIDs {
		exactMap[id] = true
	}

	for _, ch := range channels {
		for rankIdx, match := range ch {
			rank := match.Rank
			if rank <= 0 {
				rank = rankIdx + 1
			}
			rrfScores[match.MemoryID] += 1.0 / (f.config.K + float64(rank))
		}
	}

	var results []FusedResult

	for memID, rrf := range rrfScores {
		rec, hasRec := records[memID]
		var reasons []string
		reasons = append(reasons, fmt.Sprintf("rrf:base=%.5f", rrf))

		var exactBoost float64
		if exactMap[memID] {
			exactBoost = 0.20
			reasons = append(reasons, "exact_match_boost (+0.20)")
		}

		var authBoost float64
		var penalty float64

		if hasRec {
			switch rec.Authority {
			case model.AuthorityOperator:
				authBoost = 0.15
				reasons = append(reasons, "authority:operator (+0.15)")
			case model.AuthorityPolicy:
				authBoost = 0.10
				reasons = append(reasons, "authority:policy (+0.10)")
			case model.AuthorityVerified:
				authBoost = 0.05
				reasons = append(reasons, "authority:verified (+0.05)")
			}

			switch rec.Lifecycle {
			case model.MemoryStale:
				penalty = 0.30
				reasons = append(reasons, "lifecycle:stale (-0.30)")
			case model.MemoryConflicted:
				penalty = 0.40
				reasons = append(reasons, "lifecycle:conflicted (-0.40)")
			}
		}

		finalScore := rrf + exactBoost + authBoost - penalty
		if finalScore < 0 {
			finalScore = 0
		}

		results = append(results, FusedResult{
			MemoryID:  memID,
			RankScore: finalScore,
			Breakdown: ScoreBreakdown{
				RRFScore:         rrf,
				ExactMatchBoost:  exactBoost,
				AuthorityBoost:   authBoost,
				LifecyclePenalty: penalty,
				FinalRankScore:   finalScore,
				Reasons:          reasons,
			},
		})
	}

	// Deterministic sorting by FinalRankScore desc, tie-breaking by MemoryID asc
	sort.Slice(results, func(i, j int) bool {
		if results[i].RankScore != results[j].RankScore {
			return results[i].RankScore > results[j].RankScore
		}
		return results[i].MemoryID < results[j].MemoryID
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return results
}
