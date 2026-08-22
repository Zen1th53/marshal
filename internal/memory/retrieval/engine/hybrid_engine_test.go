package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/index/graph"
	"github.com/Zen1th53/marshal/internal/memory/index/lexical"
	"github.com/Zen1th53/marshal/internal/memory/index/vector"
	"github.com/Zen1th53/marshal/internal/memory/retrieval/engine"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT109ParallelMultiIndexRetrievalAndDegradation(t *testing.T) {
	lexIdx := lexical.NewLexicalIndex()
	vecStore := vector.NewLocalVectorStore()
	graphIdx := graph.NewGraphIndex()

	ctx := context.Background()
	now := time.Now().UTC()

	// Ingest test record
	rec := model.MemoryRecordV2{
		ID:         "MEM-109-A",
		ProjectID:  "PROJ-1",
		Kind:       model.MemoryKindDecision,
		Lifecycle:  model.MemoryDurable,
		Title:      "Use WAL mode",
		Body:       "Enable SQLite WAL mode for high concurrency",
		Scope:      string(model.ScopeProject),
		ScopeID:    "scope-1",
		ObservedAt: now,
		CreatedAt:  now,
	}

	_ = lexIdx.IndexRecord(ctx, rec)
	_ = vecStore.UpsertVector(ctx, "MEM-109-A", "PROJ-1", "scope-1", []float32{1.0, 0.0, 0.0})
	_ = graphIdx.AddNode(ctx, graph.GraphNode{ID: "MEM-109-A", Kind: "decision", ScopeID: "scope-1"})

	hybrid := engine.NewHybridEngine(engine.Config{
		Lexical: lexIdx,
		Vector:  vecStore,
		Graph:   graphIdx,
		Timeout: 100 * time.Millisecond,
	})

	// 1. Full healthy parallel query
	res, err := hybrid.Query(ctx, engine.QueryParams{
		ProjectID:       "PROJ-1",
		Query:           "SQLite WAL mode",
		QueryEmbedding:  []float32{1.0, 0.1, 0.0},
		AllowedScopeIDs: []string{"scope-1"},
		Limit:           10,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res.Candidates) == 0 {
		t.Fatal("expected non-empty candidate list")
	}
	if len(res.DegradedChannels) != 0 {
		t.Fatalf("expected 0 degraded channels on healthy query, got: %+v", res.DegradedChannels)
	}

	// 2. Dense vector timeout / failure graceful degradation
	failingVectorEngine := engine.NewHybridEngine(engine.Config{
		Lexical: lexIdx,
		Vector:  &failingVectorBackend{},
		Graph:   graphIdx,
		Timeout: 50 * time.Millisecond,
	})

	degradedRes, err := failingVectorEngine.Query(ctx, engine.QueryParams{
		ProjectID:       "PROJ-1",
		Query:           "SQLite WAL mode",
		QueryEmbedding:  []float32{1.0, 0.0, 0.0},
		AllowedScopeIDs: []string{"scope-1"},
		Limit:           10,
	})
	if err != nil {
		t.Fatalf("Query on degraded backend should not fail: %v", err)
	}
	if len(degradedRes.Candidates) == 0 {
		t.Fatal("expected lexical results even when vector backend fails")
	}
	if len(degradedRes.DegradedChannels) == 0 || degradedRes.DegradedChannels[0] != "vector" {
		t.Fatalf("expected vector in degraded channels, got: %+v", degradedRes.DegradedChannels)
	}

	// 3. Context cancellation
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err = hybrid.Query(cancelCtx, engine.QueryParams{
		ProjectID: "PROJ-1",
		Query:     "WAL",
	})
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

type failingVectorBackend struct{}

func (f *failingVectorBackend) Name() string    { return "failing" }
func (f *failingVectorBackend) Version() string { return "0.0.0" }
func (f *failingVectorBackend) UpsertVector(ctx context.Context, id, proj, scope string, emb []float32) error {
	return nil
}
func (f *failingVectorBackend) DeleteVector(ctx context.Context, id string) error { return nil }
func (f *failingVectorBackend) SearchVectors(ctx context.Context, proj string, scopes []string, emb []float32, limit int) ([]vector.VectorSearchResult, error) {
	time.Sleep(100 * time.Millisecond) // exceeds 50ms timeout
	return nil, context.DeadlineExceeded
}
func (f *failingVectorBackend) Rebuild(ctx context.Context, items []vector.VectorItem) error {
	return nil
}
func (f *failingVectorBackend) Health(ctx context.Context) error { return nil }
