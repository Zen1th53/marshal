package quota_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/quota"
)

func TestT126MemoryQuotasAndBackpressure(t *testing.T) {
	limiter := quota.NewLimiter(quota.Config{
		MaxWritesPerMinute: 10,
		MaxStorageBytes:    1000,
		MaxImportBatchSize: 5,
		MaxRecallTopK:      20,
	})
	ctx := context.Background()

	// 1. Write flood test
	for i := 0; i < 10; i++ {
		if err := limiter.AllowWrite(ctx, "PROJ-1", 50); err != nil {
			t.Fatalf("write %d should succeed: %v", i, err)
		}
	}
	err := limiter.AllowWrite(ctx, "PROJ-1", 50)
	if !errors.Is(err, quota.ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded on 11th write, got: %v", err)
	}

	// 2. Storage budget exceeded
	limiter2 := quota.NewLimiter(quota.Config{
		MaxWritesPerMinute: 100,
		MaxStorageBytes:    500,
	})
	err = limiter2.AllowWrite(ctx, "PROJ-2", 600)
	if !errors.Is(err, quota.ErrStorageBudgetExceeded) {
		t.Fatalf("expected ErrStorageBudgetExceeded for 600 bytes against 500 limit, got: %v", err)
	}

	// 3. Huge import batch limit
	err = limiter.ValidateImportBatch(ctx, 10)
	if !errors.Is(err, quota.ErrImportBatchTooLarge) {
		t.Fatalf("expected ErrImportBatchTooLarge for batch size 10 > 5, got: %v", err)
	}

	// 4. Broad recall clamping
	clampedK := limiter.ClampRecallK(100)
	if clampedK != 20 {
		t.Fatalf("expected broad recall top-k to be clamped to 20, got %d", clampedK)
	}
}
