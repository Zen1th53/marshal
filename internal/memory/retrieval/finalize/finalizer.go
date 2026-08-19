package finalize

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/retrieval/fusion"
	"github.com/Zen1th53/marshal/internal/model"
)

type Params struct {
	AsOf            time.Time `json:"as_of"`
	AllowedScopeIDs []string  `json:"allowed_scope_ids"`
}

type FinalCandidate struct {
	MemoryID     string                `json:"memory_id"`
	Record       model.MemoryRecordV2  `json:"record"`
	RankScore    float64               `json:"rank_score"`
	IsHistorical bool                  `json:"is_historical"`
	IsConflicted bool                  `json:"is_conflicted"`
	ConflictIDs  []string              `json:"conflict_ids,omitempty"`
}

type Finalizer struct{}

func NewFinalizer() *Finalizer {
	return &Finalizer{}
}

// Finalize evaluates candidates against lifecycle state, bitemporal valid intervals, and conflict metadata.
func (f *Finalizer) Finalize(ctx context.Context, candidates []fusion.FusedResult, records map[string]model.MemoryRecordV2, params Params) ([]FinalCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	asOf := params.AsOf
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}
	isHistoricalQuery := time.Since(asOf) > 30*24*time.Hour

	allowedScopeMap := make(map[string]bool)
	for _, sc := range params.AllowedScopeIDs {
		allowedScopeMap[sc] = true
	}

	var results []FinalCandidate

	for _, cand := range candidates {
		rec, exists := records[cand.MemoryID]
		if !exists {
			continue
		}

		// 1. Filter out tombstoned or rejected records
		if rec.Lifecycle == model.MemoryTombstoned || rec.Lifecycle == model.MemoryRejected {
			continue
		}

		// 2. Scope check
		if len(params.AllowedScopeIDs) > 0 && rec.ScopeID != "" && !allowedScopeMap[rec.ScopeID] {
			continue
		}

		// 3. Temporal validity checks
		if !rec.ValidFrom.IsZero() && rec.ValidFrom.After(asOf) {
			continue // Not yet valid at query time
		}

		isSupersededAtQueryTime := rec.ValidTo != nil && rec.ValidTo.Before(asOf)

		if isSupersededAtQueryTime {
			if !isHistoricalQuery && rec.Lifecycle == model.MemorySuperseded {
				// Suppress superseded records on current-time query
				continue
			}
		}

		isConflicted := rec.Lifecycle == model.MemoryConflicted
		var conflictIDs []string
		if isConflicted {
			conflictIDs = rec.ConflictIDs
		}

		results = append(results, FinalCandidate{
			MemoryID:     rec.ID,
			Record:       rec,
			RankScore:    cand.RankScore,
			IsHistorical: isSupersededAtQueryTime || isHistoricalQuery,
			IsConflicted: isConflicted,
			ConflictIDs:  conflictIDs,
		})
	}

	return results, nil
}
