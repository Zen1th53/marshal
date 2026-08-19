package temporal

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// Evaluator provides bitemporal logic for valid time vs known time.
type Evaluator struct{}

func NewEvaluator() *Evaluator {
	return &Evaluator{}
}

// IsActiveAt returns true if the memory record was known to the system at knownAt,
// and is within its valid interval at validAsOf.
func (e *Evaluator) IsActiveAt(ctx context.Context, rec model.MemoryRecordV2, validAsOf, knownAt time.Time) bool {
	// Check known time (ingestion time)
	if !knownAt.IsZero() && rec.IngestedAt.After(knownAt) {
		return false
	}

	// Check valid time start
	if !validAsOf.IsZero() && rec.ValidFrom.After(validAsOf) {
		return false
	}

	// Check valid time end
	if !validAsOf.IsZero() && rec.ValidTo != nil && !rec.ValidTo.IsZero() && rec.ValidTo.Before(validAsOf) {
		return false
	}

	return true
}

// CloseValidity returns an updated record with ValidTo set to closedAt.
func (e *Evaluator) CloseValidity(rec model.MemoryRecordV2, closedAt time.Time) model.MemoryRecordV2 {
	res := rec
	t := closedAt.UTC()
	res.ValidTo = &t
	return res
}
