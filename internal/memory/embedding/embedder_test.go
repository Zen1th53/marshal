package embedding_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/embedding"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT105EmbeddingLifecycleAndRemotePolicy(t *testing.T) {
	mgr := embedding.NewManager()
	ctx := context.Background()

	// 1. Local deterministic embedder produces stable vectors
	localEmb := embedding.NewDeterministicEmbedder("local-hash-v1", 128)
	vec1, err := localEmb.Embed(ctx, "Memory record body text")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec1) != 128 {
		t.Fatalf("expected 128 dimensions, got %d", len(vec1))
	}

	// Stability: same text produces identical embedding
	vec2, _ := localEmb.Embed(ctx, "Memory record body text")
	for i := range vec1 {
		if vec1[i] != vec2[i] {
			t.Fatalf("embedding not deterministic at index %d", i)
		}
	}

	// 2. Remote embedding forbidden policy check on protected memory
	remoteEmb := embedding.NewRemoteEmbedder("text-embedding-3-small", 1536, false) // remote not allowed
	protectedRec := model.MemoryRecordV2{
		ID:        "MEM-SEC-01",
		Kind:      model.MemoryKindDecision,
		Authority: model.AuthorityOperator,
		Body:      "Secret architecture decision",
	}

	_, err = mgr.EmbedRecord(ctx, remoteEmb, protectedRec)
	if !errors.Is(err, embedding.ErrRemoteEmbeddingForbidden) {
		t.Fatalf("expected ErrRemoteEmbeddingForbidden, got: %v", err)
	}

	// 3. Allowed remote embedding succeeds
	allowedRemoteEmb := embedding.NewRemoteEmbedder("text-embedding-3-small", 1536, true)
	vecRemote, err := mgr.EmbedRecord(ctx, allowedRemoteEmb, protectedRec)
	if err != nil {
		t.Fatalf("EmbedRecord with allowed remote embedder: %v", err)
	}
	if len(vecRemote) != 1536 {
		t.Fatalf("expected 1536 dimensions, got %d", len(vecRemote))
	}
}
