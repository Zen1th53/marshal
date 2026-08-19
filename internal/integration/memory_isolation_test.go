package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/index/graph"
	"github.com/Zen1th53/marshal/internal/memory/index/lexical"
	"github.com/Zen1th53/marshal/internal/memory/index/vector"
	"github.com/Zen1th53/marshal/internal/memory/retrieval/disclosure"
	"github.com/Zen1th53/marshal/internal/memory/retrieval/engine"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT124CrossScopeTenantIsolationAdversarialSuite(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	// 1. Setup Tenant A and Tenant B data
	tenantARecord := model.MemoryRecordV2{
		ID:        "MEM-TENANT-A-SECRET",
		ProjectID: "PROJ-A",
		Scope:     string(model.ScopeProject),
		ScopeID:   "scope-tenant-a",
		Title:     "Confidential Tenant A Infrastructure Key",
		Body:      "Secret Token for Tenant A Database Cluster",
		Lifecycle: model.MemoryDurable,
	}

	tenantBRecord := model.MemoryRecordV2{
		ID:        "MEM-TENANT-B-DATA",
		ProjectID: "PROJ-B",
		Scope:     string(model.ScopeProject),
		ScopeID:   "scope-tenant-b",
		Title:     "Tenant B General Docs",
		Body:      "Public documentation for Tenant B services",
		Lifecycle: model.MemoryDurable,
	}

	// 2. Index in Lexical, Vector, and Graph
	lexIdx := lexical.NewLexicalIndex()
	_ = lexIdx.IndexRecord(ctx, tenantARecord)
	_ = lexIdx.IndexRecord(ctx, tenantBRecord)

	vecStore := vector.NewLocalVectorStore()
	_ = vecStore.UpsertVector(ctx, tenantARecord.ID, "PROJ-A", "scope-tenant-a", []float32{1.0, 0.0, 0.0})
	_ = vecStore.UpsertVector(ctx, tenantBRecord.ID, "PROJ-B", "scope-tenant-b", []float32{1.0, 0.0, 0.0})

	graphIdx := graph.NewGraphIndex()
	_ = graphIdx.AddNode(ctx, graph.GraphNode{ID: tenantARecord.ID, ScopeID: "scope-tenant-a", Kind: "secret"})
	_ = graphIdx.AddNode(ctx, graph.GraphNode{ID: tenantBRecord.ID, ScopeID: "scope-tenant-b", Kind: "doc"})
	_ = graphIdx.AddEdge(ctx, graph.GraphEdge{
		FromID:   tenantARecord.ID,
		ToID:     "SHARED-ENTITY-DB",
		Relation: "references",
	})
	_ = graphIdx.AddEdge(ctx, graph.GraphEdge{
		FromID:   tenantBRecord.ID,
		ToID:     "SHARED-ENTITY-DB",
		Relation: "references",
	})

	hybridEngine := engine.NewHybridEngine(engine.Config{
		Lexical: lexIdx,
		Vector:  vecStore,
		Graph:   graphIdx,
	})

	// 3. Adversarial Probe 1: Vector Search by Tenant B with identical embedding
	vecResults, err := vecStore.SearchVectors(ctx, "PROJ-B", []string{"scope-tenant-b"}, []float32{1.0, 0.0, 0.0}, 10)
	if err != nil {
		t.Fatalf("SearchVectors: %v", err)
	}
	for _, r := range vecResults {
		if r.MemoryID == "MEM-TENANT-A-SECRET" {
			t.Fatal("CRITICAL LEAK: Tenant B vector search disclosed Tenant A secret memory!")
		}
	}

	// 4. Adversarial Probe 2: Graph Traversal from shared node name
	graphNodes, _, err := graphIdx.Traverse(ctx, []string{"SHARED-ENTITY-DB"}, []string{"scope-tenant-b"}, now, 2)
	if err != nil {
		t.Fatalf("Graph Traverse: %v", err)
	}
	for _, n := range graphNodes {
		if n.ID == "MEM-TENANT-A-SECRET" || n.ScopeID == "scope-tenant-a" {
			t.Fatal("CRITICAL LEAK: Graph traversal disclosed Tenant A entity across scope boundary!")
		}
	}

	// 5. Adversarial Probe 3: Hybrid Engine Query by Tenant B
	hybridRes, err := hybridEngine.Query(ctx, engine.QueryParams{
		ProjectID:       "PROJ-B",
		Query:           "Secret Token Database Cluster",
		QueryEmbedding:  []float32{1.0, 0.0, 0.0},
		AllowedScopeIDs: []string{"scope-tenant-b"},
		Limit:           10,
	})
	if err != nil {
		t.Fatalf("Hybrid Query: %v", err)
	}
	for _, c := range hybridRes.Candidates {
		if c.MemoryID == "MEM-TENANT-A-SECRET" {
			t.Fatal("CRITICAL LEAK: Hybrid engine disclosed Tenant A candidate to Tenant B!")
		}
	}

	// 6. Adversarial Probe 4: Direct ID Guessing in Progressive Disclosure Level 2
	discEngine := disclosure.NewEngine(disclosure.Config{})
	recordsMap := map[string]model.MemoryRecordV2{
		tenantARecord.ID: tenantARecord,
		tenantBRecord.ID: tenantBRecord,
	}

	_, err = discEngine.DiscloseLevel2(ctx, "MEM-TENANT-A-SECRET", []string{"scope-tenant-b"}, func(id string) (model.MemoryRecordV2, bool) {
		r, ok := recordsMap[id]
		return r, ok
	})
	if err == nil {
		t.Fatal("CRITICAL LEAK: Direct ID guessing by unauthorized tenant succeeded in Level 2 disclosure!")
	}
}
