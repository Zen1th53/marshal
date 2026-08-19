package graph_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/index/graph"
)

func TestT106TemporalKnowledgeGraphTraversalAndScopeIsolation(t *testing.T) {
	idx := graph.NewGraphIndex()
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	// 1. Add Nodes
	_ = idx.AddNode(ctx, graph.GraphNode{ID: "MEM-DEC-1", Kind: "decision", ScopeID: "scope-main"})
	_ = idx.AddNode(ctx, graph.GraphNode{ID: "FILE-STORE", Kind: "file", ScopeID: "scope-main"})
	_ = idx.AddNode(ctx, graph.GraphNode{ID: "EVID-TEST-1", Kind: "evidence", ScopeID: "scope-main"})
	_ = idx.AddNode(ctx, graph.GraphNode{ID: "MEM-SEC-PRIVATE", Kind: "memory", ScopeID: "scope-private"})

	// 2. Add Temporal Edges
	// MEM-DEC-1 -> touches -> FILE-STORE (valid from t0 indefinitely)
	_ = idx.AddEdge(ctx, graph.GraphEdge{
		FromID:    "MEM-DEC-1",
		ToID:      "FILE-STORE",
		Relation:  "touches",
		ValidFrom: t0,
	})

	// MEM-DEC-1 -> evidenced_by -> EVID-TEST-1 (valid from t0 until t1, superseded at t1)
	_ = idx.AddEdge(ctx, graph.GraphEdge{
		FromID:    "MEM-DEC-1",
		ToID:      "EVID-TEST-1",
		Relation:  "evidenced_by",
		ValidFrom: t0,
		ValidTo:   &t1,
	})

	// Private edge into scope-private
	_ = idx.AddEdge(ctx, graph.GraphEdge{
		FromID:    "MEM-DEC-1",
		ToID:      "MEM-SEC-PRIVATE",
		Relation:  "references",
		ValidFrom: t0,
	})

	// 3. Traversal at t0 with "scope-main" allowlist
	nodesT0, edgesT0, err := idx.Traverse(ctx, []string{"MEM-DEC-1"}, []string{"scope-main"}, t0, 2)
	if err != nil {
		t.Fatalf("Traverse at t0: %v", err)
	}
	if len(nodesT0) != 3 { // MEM-DEC-1, FILE-STORE, EVID-TEST-1
		t.Fatalf("expected 3 nodes at t0, got %d", len(nodesT0))
	}
	if len(edgesT0) != 2 {
		t.Fatalf("expected 2 active edges at t0, got %d", len(edgesT0))
	}

	// 4. Temporal supersession: Traversal at t2 (after t1) must NOT include EVID-TEST-1 edge
	_, edgesT2, err := idx.Traverse(ctx, []string{"MEM-DEC-1"}, []string{"scope-main"}, t2, 2)
	if err != nil {
		t.Fatalf("Traverse at t2: %v", err)
	}
	if len(edgesT2) != 1 || edgesT2[0].ToID != "FILE-STORE" {
		t.Fatalf("expected only 1 edge (FILE-STORE) after t1 expiry, got: %+v", edgesT2)
	}

	// 5. Scope Isolation: Private node MUST NOT appear when only scope-main is allowed
	for _, n := range nodesT0 {
		if n.ID == "MEM-SEC-PRIVATE" {
			t.Fatal("scope leakage: private node disclosed during public traversal")
		}
	}

	// 6. Delete/Tombstone cascade
	_ = idx.RemoveNode(ctx, "FILE-STORE")
	nodesAfterDel, _, _ := idx.Traverse(ctx, []string{"MEM-DEC-1"}, []string{"scope-main"}, t0, 2)
	for _, n := range nodesAfterDel {
		if n.ID == "FILE-STORE" {
			t.Fatal("deleted node still present in traversal")
		}
	}
}
