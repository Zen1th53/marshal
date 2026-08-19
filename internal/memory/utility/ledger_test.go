package utility_test

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/utility"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT142OutcomeBasedMemoryUtilityLedger(t *testing.T) {
	ctx := context.Background()
	ledger := utility.NewLedger()

	// 1. Record repeated successful usage for procedure memory
	memID := "MEM-SKILL-WAL-FIX"
	for i := 0; i < 5; i++ {
		ledger.RecordOutcome(ctx, memID, "TASK-SUCCESS", true, true)
	}

	scoreUseful := ledger.GetUtilityScore(ctx, memID)
	if scoreUseful <= 0.5 {
		t.Fatalf("expected utility score > 0.5 after repeated success, got: %f", scoreUseful)
	}

	// 2. Record repeated failures for harmful / buggy memory
	harmfulID := "MEM-BAD-SQL-ADVICE"
	for i := 0; i < 5; i++ {
		ledger.RecordOutcome(ctx, harmfulID, "TASK-FAIL", false, false)
	}

	scoreHarmful := ledger.GetUtilityScore(ctx, harmfulID)
	if scoreHarmful >= 0.3 {
		t.Fatalf("expected degraded utility score < 0.3 after repeated failure, got: %f", scoreHarmful)
	}

	// 3. Invariant: Truth authority is strictly separate from utility score
	candidateRec := model.MemoryRecordV2{
		ID:        memID,
		Authority: model.AuthorityAgent,
		Lifecycle: model.MemoryCandidate,
	}
	durableRec := model.MemoryRecordV2{
		ID:        "MEM-DURABLE-POLICY",
		Authority: model.AuthorityOperator,
		Lifecycle: model.MemoryDurable,
	}

	effectiveRank := ledger.CalculateRankBoost(candidateRec, scoreUseful)
	durableRank := ledger.CalculateRankBoost(durableRec, 0.5)

	if effectiveRank >= durableRank {
		t.Fatalf("candidate with high utility (%f) must not outrank operator durable record (%f)", effectiveRank, durableRank)
	}
}
