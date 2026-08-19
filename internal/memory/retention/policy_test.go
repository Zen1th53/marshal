package retention_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/retention"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT92StalenessEvaluationOnSourceChange(t *testing.T) {
	eval := retention.NewPolicyEvaluator(retention.PolicyConfig{
		WorkingMemoryTTL: 24 * time.Hour,
	})
	ctx := context.Background()
	now := time.Now().UTC()

	// 1. Memory bound to commit-1 becomes stale when current head is commit-2
	rec := model.MemoryRecordV2{
		ID:         "MEM-STALE-01",
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryDurable,
		HeadCommit: "commit-1111",
		ObservedAt: now.Add(-48 * time.Hour),
		ValidFrom:  now.Add(-48 * time.Hour),
	}

	isStale, reason := eval.CheckStaleness(ctx, rec, "commit-2222", now)
	if !isStale {
		t.Fatal("expected record with changed commit to be flagged stale")
	}
	if reason == "" {
		t.Fatal("expected reason for staleness")
	}

	// 2. Working memory past TTL is marked stale
	workingRec := model.MemoryRecordV2{
		ID:         "MEM-WORK-01",
		Kind:       model.MemoryKindWorking,
		Lifecycle:  model.MemoryCandidate,
		ObservedAt: now.Add(-30 * time.Hour), // Older than 24h TTL
		ValidFrom:  now.Add(-30 * time.Hour),
	}
	isWorkingStale, _ := eval.CheckStaleness(ctx, workingRec, "", now)
	if !isWorkingStale {
		t.Fatal("expected working memory past TTL to be flagged stale")
	}
}

func TestT92TombstonePurge(t *testing.T) {
	eval := retention.NewPolicyEvaluator(retention.PolicyConfig{})

	rec := model.MemoryRecordV2{
		ID:        "MEM-PURGE-01",
		Kind:      model.MemoryKindSemantic,
		Lifecycle: model.MemoryDurable,
		Body:      "Sensitive or deprecated secret payload",
	}

	tombstoned := eval.TombstoneRecord(rec, "User requested GDPR deletion", true)
	if tombstoned.Lifecycle != model.MemoryTombstoned {
		t.Fatalf("expected lifecycle Tombstoned, got: %s", tombstoned.Lifecycle)
	}
	if tombstoned.Body != "[PURGED]" {
		t.Fatalf("expected purged body, got %q", tombstoned.Body)
	}
	if tombstoned.ExtMeta["tombstone_reason"] != "User requested GDPR deletion" {
		t.Fatalf("expected tombstone reason in ext_meta")
	}
}
