package sqlitevec_test

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/index/vector/sqlitevec"
)

func TestT103SQLiteVecBackendGracefulDegradationAndScopeIsolation(t *testing.T) {
	backend := sqlitevec.NewBackend(sqlitevec.Config{
		ExpectedVersion: "0.1.6",
	})
	ctx := context.Background()

	// 1. Health check reports operational fallback status
	if err := backend.Health(ctx); err != nil {
		t.Fatalf("Health check failed: %v", err)
	}

	// 2. Insert records with different scopes
	embAlpha := []float32{1.0, 0.0, 0.0}
	embBeta := []float32{0.0, 1.0, 0.0}

	if err := backend.UpsertVector(ctx, "MEM-1", "PROJ-1", "scope-alpha", embAlpha); err != nil {
		t.Fatalf("UpsertVector 1: %v", err)
	}
	if err := backend.UpsertVector(ctx, "MEM-2", "PROJ-1", "scope-beta", embBeta); err != nil {
		t.Fatalf("UpsertVector 2: %v", err)
	}

	// 3. Cross-scope leakage check: querying for Alpha scope MUST NOT disclose Beta scope
	results, err := backend.SearchVectors(ctx, "PROJ-1", []string{"scope-alpha"}, embBeta, 10)
	if err != nil {
		t.Fatalf("SearchVectors: %v", err)
	}
	for _, res := range results {
		if res.MemoryID == "MEM-2" {
			t.Fatalf("cross-scope leakage detected: MEM-2 from scope-beta disclosed when only scope-alpha allowed")
		}
	}

	// 4. Version mismatch health failure check
	mismatchedBackend := sqlitevec.NewBackend(sqlitevec.Config{
		ExpectedVersion: "99.9.9", // incompatible version
	})
	if err := mismatchedBackend.Health(ctx); err == nil {
		t.Fatal("expected health check to fail on version mismatch")
	}
}
