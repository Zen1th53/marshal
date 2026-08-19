package governance_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/governance"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT146ForgettingAwareGovernance(t *testing.T) {
	ctx := context.Background()
	gov := governance.NewForgettingManager()
	now := time.Now().UTC()
	past := now.Add(-48 * time.Hour)

	// 1. Setup superseded memory record
	obsoletePolicy := model.MemoryRecordV2{
		ID:         "MEM-POLICY-OLD",
		ProjectID:  "PROJ-1",
		Title:      "Legacy Auth Policy",
		Body:       "Use plain MD5 tokens for internal auth",
		Lifecycle:  model.MemorySuperseded,
		ValidFrom:  past,
		ValidTo:    &now,
		ObservedAt: past,
	}

	activePolicy := model.MemoryRecordV2{
		ID:         "MEM-POLICY-NEW",
		ProjectID:  "PROJ-1",
		Title:      "Current Auth Policy",
		Body:       "Use Ed25519 signed JWT tokens for internal auth",
		Lifecycle:  model.MemoryDurable,
		ValidFrom:  now,
		ObservedAt: now,
	}

	// 2. Normal current-time recall must filter out obsoletePolicy
	resultsCurrent, err := gov.FilterForgetting(ctx, []model.MemoryRecordV2{obsoletePolicy, activePolicy}, governance.QueryContext{
		IncludeHistory: false,
	})
	if err != nil {
		t.Fatalf("FilterForgetting: %v", err)
	}
	if len(resultsCurrent) != 1 || resultsCurrent[0].ID != "MEM-POLICY-NEW" {
		t.Fatalf("expected only active policy in current recall, got: %+v", resultsCurrent)
	}

	// 3. Historical As-Of query includes historical record with explicit warning label
	resultsHistory, err := gov.FilterForgetting(ctx, []model.MemoryRecordV2{obsoletePolicy, activePolicy}, governance.QueryContext{
		IncludeHistory: true,
		AsOf:           &past,
	})
	if err != nil {
		t.Fatalf("FilterForgetting AsOf: %v", err)
	}
	if len(resultsHistory) != 2 {
		t.Fatalf("expected 2 records in historical recall, got %d", len(resultsHistory))
	}
	if !resultsHistory[0].IsHistoricalWarning {
		t.Fatal("expected IsHistoricalWarning on superseded historical record")
	}

	// 4. Invalidation penalty check: False retention triggers conformance error
	err = gov.VerifyNoFalseRetention(ctx, []string{"MEM-POLICY-OLD"}, map[string]model.MemoryLifecycle{
		"MEM-POLICY-OLD": model.MemorySuperseded,
	})
	if !errors.Is(err, governance.ErrFalseRetentionDetected) {
		t.Fatalf("expected ErrFalseRetentionDetected when obsolete record is retained in active context, got: %v", err)
	}
}
