package cache_test

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/retrieval/cache"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT163RetrievalCacheAndInvalidation(t *testing.T) {
	c := cache.NewBoundedCache(cache.Config{MaxEntries: 100, TTL: 5 * time.Minute})

	queryKey := cache.QueryKey{
		ProjectID: "PROJ-CACHE-01",
		ScopeIDs:  []string{"PROJ-CACHE-01"},
		Query:     "SQLite WAL pragma",
		TopK:      5,
	}

	results := []model.MemoryRecordV2{
		{
			ID:        "MEM-CACHE-1",
			ProjectID: "PROJ-CACHE-01",
			Title:     "SQLite WAL Configuration",
			Body:      "PRAGMA journal_mode=WAL;",
		},
	}

	// 1. Initial Cache Miss
	_, hit := c.Get(queryKey)
	if hit {
		t.Fatal("expected cache miss on empty cache")
	}

	// 2. Put and Cache Hit
	c.Put(queryKey, results)
	cached, hit := c.Get(queryKey)
	if !hit || len(cached) != 1 || cached[0].ID != "MEM-CACHE-1" {
		t.Fatalf("expected cache hit with 1 record, got hit=%v, cached=%+v", hit, cached)
	}

	// 3. Invalidate on Mutation/Deletion for Project Scope
	c.InvalidateScope("PROJ-CACHE-01")
	_, hitAfterInvalidation := c.Get(queryKey)
	if hitAfterInvalidation {
		t.Fatal("expected cache miss after scope invalidation")
	}
}
