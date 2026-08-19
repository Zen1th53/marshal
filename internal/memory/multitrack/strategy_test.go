package multitrack_test

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/multitrack"
)

func TestT145WorkloadAdaptiveMultiTrackStrategy(t *testing.T) {
	ctx := context.Background()
	router := multitrack.NewRouter()

	// 1. Exact symbol task favors TrackCodeSymbol
	allocSymbol := router.AllocateTrackBudget(ctx, "func OpenDB in internal/store/migrations.go", 4000)
	if allocSymbol.PrimaryTrack != multitrack.TrackCodeSymbol {
		t.Fatalf("expected TrackCodeSymbol for function name, got: %s", allocSymbol.PrimaryTrack)
	}
	if allocSymbol.TrackBudgets[multitrack.TrackCodeSymbol] <= allocSymbol.TrackBudgets[multitrack.TrackProceduralWorkflow] {
		t.Fatalf("expected code track budget > procedural track budget for code symbol query")
	}

	// 2. Repeated workflow task favors TrackProceduralWorkflow
	allocWorkflow := router.AllocateTrackBudget(ctx, "How to build and test release package with release_verify.py", 4000)
	if allocWorkflow.PrimaryTrack != multitrack.TrackProceduralWorkflow {
		t.Fatalf("expected TrackProceduralWorkflow for build workflow, got: %s", allocWorkflow.PrimaryTrack)
	}

	// 3. Environment gotcha favors TrackEnvironmentGotcha
	allocEnv := router.AllocateTrackBudget(ctx, "CGO_ENABLED=0 build failure with sqlite3 driver error", 4000)
	if allocEnv.PrimaryTrack != multitrack.TrackEnvironmentGotcha {
		t.Fatalf("expected TrackEnvironmentGotcha for CGO error, got: %s", allocEnv.PrimaryTrack)
	}

	// 4. Track budget allocation sum does not exceed total budget
	totalAlloc := 0
	for _, b := range allocSymbol.TrackBudgets {
		totalAlloc += b
	}
	if totalAlloc > 4000 {
		t.Fatalf("track budget sum %d exceeds total budget 4000", totalAlloc)
	}
}
