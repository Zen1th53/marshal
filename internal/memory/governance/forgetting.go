package governance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrFalseRetentionDetected = errors.New("conformance failure: obsolete/superseded memory retained in live context")
)

type QueryContext struct {
	IncludeHistory bool       `json:"include_history"`
	AsOf           *time.Time `json:"as_of,omitempty"`
}

type GovernedMemoryResult struct {
	ID                  string               `json:"id"`
	Record              model.MemoryRecordV2 `json:"record"`
	IsHistoricalWarning bool                 `json:"is_historical_warning"`
	WarningMessage      string               `json:"warning_message,omitempty"`
}

type ForgettingManager struct{}

func NewForgettingManager() *ForgettingManager {
	return &ForgettingManager{}
}

// FilterForgetting applies lifecycle suppression to ensure live queries never recall obsolete memory.
func (m *ForgettingManager) FilterForgetting(ctx context.Context, records []model.MemoryRecordV2, qCtx QueryContext) ([]GovernedMemoryResult, error) {
	var results []GovernedMemoryResult

	for _, rec := range records {
		if rec.Lifecycle == model.MemoryTombstoned || rec.Lifecycle == model.MemoryRejected {
			continue // hard filtered
		}

		if rec.Lifecycle == model.MemorySuperseded || rec.Lifecycle == model.MemoryStale {
			if !qCtx.IncludeHistory {
				continue // suppressed in live context
			}

			// In historical query mode, include with warning
			results = append(results, GovernedMemoryResult{
				ID:                  rec.ID,
				Record:              rec,
				IsHistoricalWarning: true,
				WarningMessage:      "[HISTORICAL_RECORD_OBSOLETE_DO_NOT_ACT_AS_CURRENT_POLICY]",
			})
			continue
		}

		results = append(results, GovernedMemoryResult{
			ID:                  rec.ID,
			Record:              rec,
			IsHistoricalWarning: false,
		})
	}

	return results, nil
}

// VerifyNoFalseRetention enforces strict conformance: no superseded or stale record may be included in active prompts.
func (m *ForgettingManager) VerifyNoFalseRetention(ctx context.Context, includedMemoryIDs []string, lifecycleMap map[string]model.MemoryLifecycle) error {
	for _, id := range includedMemoryIDs {
		lc, ok := lifecycleMap[id]
		if ok && (lc == model.MemorySuperseded || lc == model.MemoryStale || lc == model.MemoryTombstoned) {
			return fmt.Errorf("%w: record %s has lifecycle %s", ErrFalseRetentionDetected, id, lc)
		}
	}
	return nil
}
