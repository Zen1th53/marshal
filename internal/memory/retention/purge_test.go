package retention_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/index/graph"
	"github.com/Zen1th53/marshal/internal/memory/index/lexical"
	"github.com/Zen1th53/marshal/internal/memory/index/vector"
	"github.com/Zen1th53/marshal/internal/memory/retention"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT125MemoryPurgeAndIndexErasure(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	lexIdx := lexical.NewLexicalIndex()
	vecStore := vector.NewLocalVectorStore()
	graphIdx := graph.NewGraphIndex()

	rec := model.MemoryRecordV2{
		ID:        "MEM-PURGE-TARGET-1",
		ProjectID: "PROJ-1",
		ScopeID:   "scope-1",
		Title:     "Old Temporary Password",
		Body:      "Secret credentials to be purged immediately",
		Lifecycle: model.MemoryDurable,
	}

	_ = lexIdx.IndexRecord(ctx, rec)
	_ = vecStore.UpsertVector(ctx, rec.ID, "PROJ-1", "scope-1", []float32{1.0, 0.0, 0.0})
	_ = graphIdx.AddNode(ctx, graph.GraphNode{ID: rec.ID, ScopeID: "scope-1", Kind: "secret"})

	purgeMgr := retention.NewPurgeManager(retention.PurgeConfig{
		Lexical: lexIdx,
		Vector:  vecStore,
		Graph:   graphIdx,
	})

	// 1. Execute hard purge
	err := purgeMgr.HardPurge(ctx, "PROJ-1", rec.ID)
	if err != nil {
		t.Fatalf("HardPurge: %v", err)
	}

	// 2. Search Lexical index -> must be empty
	lexResults, _ := lexIdx.Search(ctx, "PROJ-1", "Temporary Password", 10)
	if len(lexResults) != 0 {
		t.Fatalf("expected 0 lexical results after hard purge, got %d", len(lexResults))
	}

	// 3. Search Vector backend -> must be empty
	vecResults, _ := vecStore.SearchVectors(ctx, "PROJ-1", []string{"scope-1"}, []float32{1.0, 0.0, 0.0}, 10)
	if len(vecResults) != 0 {
		t.Fatalf("expected 0 vector results after hard purge, got %d", len(vecResults))
	}

	// 4. Graph Traversal -> must not find node
	nodes, _, _ := graphIdx.Traverse(ctx, []string{rec.ID}, []string{"scope-1"}, now, 1)
	if len(nodes) != 0 {
		t.Fatalf("expected 0 graph nodes after hard purge, got %d", len(nodes))
	}

	// 5. Rebuild protection: Purge ledger prevents re-indexing during rebuild
	if !purgeMgr.IsPurged(rec.ID) {
		t.Fatal("expected ID to be recorded in purge ledger")
	}
}
