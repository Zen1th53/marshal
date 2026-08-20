package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT184TaskDAGDeterministicLayout(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/dag?max_depth=50", nil)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", w.Code)
	}

	var dag webcontrol.TaskDAGResponseDTO
	if err := json.NewDecoder(w.Body).Decode(&dag); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// 1. Max depth clamped to 10
	if dag.MaxDepth != 10 {
		t.Fatalf("expected max depth clamped to 10, got %d", dag.MaxDepth)
	}

	// 2. Nodes & edges populated
	if len(dag.Nodes) < 3 || len(dag.Edges) < 2 {
		t.Fatalf("expected at least 3 nodes and 2 edges, got: %d nodes, %d edges", len(dag.Nodes), len(dag.Edges))
	}

	// 3. Layer determinism: Root must have Layer 0
	if dag.Nodes[0].Layer != 0 {
		t.Fatalf("expected root node to have layer 0, got %d", dag.Nodes[0].Layer)
	}
}
