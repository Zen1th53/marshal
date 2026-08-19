package temporal_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/temporal"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT89BitemporalEvaluation(t *testing.T) {
	evaluator := temporal.NewEvaluator()
	ctx := context.Background()

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	// Record valid from t0 to t2, ingested at t1
	rec := model.MemoryRecordV2{
		ID:         "MEM-TIME-01",
		ProjectID:  "PROJ-1",
		ValidFrom:  t0,
		ValidTo:    &t2,
		IngestedAt: t1,
	}

	// 1. Query as of (t1, t1) -> Valid and Known
	if !evaluator.IsActiveAt(ctx, rec, t1, t1) {
		t.Fatal("expected record to be active at (t1, t1)")
	}

	// 2. Query as of (t0, t0) -> Valid in world, but NOT yet known to system at t0 (ingested at t1)
	if evaluator.IsActiveAt(ctx, rec, t0, t0) {
		t.Fatal("record was not yet known to system at t0")
	}

	// 3. Query as of (t3, t3) -> Expired in validity at t3 (valid_to is t2)
	if evaluator.IsActiveAt(ctx, rec, t3, t3) {
		t.Fatal("record expired at t2, should not be valid at t3")
	}
}

func TestT89CloseValidityInterval(t *testing.T) {
	evaluator := temporal.NewEvaluator()

	now := time.Now().UTC()
	openRec := model.MemoryRecordV2{
		ID:        "MEM-OPEN-01",
		ValidFrom: now.Add(-24 * time.Hour),
		ValidTo:   nil, // Open-ended
	}

	closed := evaluator.CloseValidity(openRec, now)
	if closed.ValidTo == nil {
		t.Fatal("expected valid_to to be set after closing interval")
	}
	if !closed.ValidTo.Equal(now) {
		t.Fatalf("expected valid_to %v, got %v", now, *closed.ValidTo)
	}
}
