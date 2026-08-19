package external_test

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/conformance/memory/external"
)

func TestT134ExternalMemoryBenchmarkAdapters(t *testing.T) {
	ctx := context.Background()

	cfg := external.AdapterConfig{
		Dataset:     "locomo",
		Model:       "codex-eval",
		Embedding:   "v2-multimodal-dense",
		TopK:        10,
		TokenBudget: 4096,
	}

	adapter := external.NewBenchmarkAdapter(cfg)

	// 1. Config manifest hashing is deterministic
	hash1 := adapter.ConfigDigest()
	hash2 := adapter.ConfigDigest()
	if hash1 == "" || hash1 != hash2 {
		t.Fatalf("expected deterministic config digest, got %q vs %q", hash1, hash2)
	}

	// 2. Smoke run subset
	run, err := adapter.RunSmoke(ctx, 3)
	if err != nil {
		t.Fatalf("RunSmoke: %v", err)
	}
	if run.CompletedCount != 3 {
		t.Fatalf("expected 3 completed scenarios, got: %d", run.CompletedCount)
	}

	// 3. Checkpoint and Resume
	checkpoint := run.CreateCheckpoint()
	adapterResumed := external.NewBenchmarkAdapter(cfg)
	resumedRun, err := adapterResumed.Resume(ctx, checkpoint, 5)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumedRun.CompletedCount != 5 {
		t.Fatalf("expected total 5 completed after resume from 3, got: %d", resumedRun.CompletedCount)
	}
}
