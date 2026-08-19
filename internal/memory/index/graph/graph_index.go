package graph

import (
	"context"
	"sync"
	"time"
)

type GraphNode struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"` // "memory", "evidence", "task", "file", "decision"
	ScopeID string   `json:"scope_id"`
	Labels  []string `json:"labels,omitempty"`
}

type GraphEdge struct {
	FromID     string     `json:"from_id"`
	ToID       string     `json:"to_id"`
	Relation   string     `json:"relation"` // "touches", "evidenced_by", "references", "supersedes"
	ValidFrom  time.Time  `json:"valid_from"`
	ValidTo    *time.Time `json:"valid_to,omitempty"`
	Confidence float64    `json:"confidence"`
}

type GraphIndex struct {
	mu    sync.RWMutex
	nodes map[string]GraphNode
	edges []GraphEdge
}

func NewGraphIndex() *GraphIndex {
	return &GraphIndex{
		nodes: make(map[string]GraphNode),
		edges: nil,
	}
}

func (g *GraphIndex) AddNode(ctx context.Context, node GraphNode) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.nodes[node.ID] = node
	return nil
}

func (g *GraphIndex) AddEdge(ctx context.Context, edge GraphEdge) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.edges = append(g.edges, edge)
	return nil
}

func (g *GraphIndex) RemoveNode(ctx context.Context, nodeID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	delete(g.nodes, nodeID)
	// Remove incident edges
	var activeEdges []GraphEdge
	for _, e := range g.edges {
		if e.FromID != nodeID && e.ToID != nodeID {
			activeEdges = append(activeEdges, e)
		}
	}
	g.edges = activeEdges
	return nil
}

// Traverse performs bounded point-in-time BFS neighborhood search starting from seedNodeIDs within allowedScopeIDs.
func (g *GraphIndex) Traverse(ctx context.Context, seedNodeIDs []string, allowedScopeIDs []string, asOf time.Time, maxDepth int) ([]GraphNode, []GraphEdge, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	allowedScopeMap := make(map[string]bool)
	for _, sc := range allowedScopeIDs {
		allowedScopeMap[sc] = true
	}

	visitedNodes := make(map[string]bool)
	var resultNodes []GraphNode
	var resultEdges []GraphEdge

	queue := seedNodeIDs

	// Seed nodes validation
	for _, seedID := range seedNodeIDs {
		if node, ok := g.nodes[seedID]; ok {
			if len(allowedScopeIDs) == 0 || allowedScopeMap[node.ScopeID] {
				visitedNodes[seedID] = true
				resultNodes = append(resultNodes, node)
			}
		}
	}

	for depth := 0; depth < maxDepth && len(queue) > 0; depth++ {
		var nextQueue []string

		for _, currentID := range queue {
			for _, edge := range g.edges {
				if edge.FromID != currentID {
					continue
				}

				// Check temporal validity
				if !edge.ValidFrom.IsZero() && edge.ValidFrom.After(asOf) {
					continue
				}
				if edge.ValidTo != nil && edge.ValidTo.Before(asOf) {
					continue
				}

				targetNode, ok := g.nodes[edge.ToID]
				if !ok {
					continue
				}

				// Scope check
				if len(allowedScopeIDs) > 0 && !allowedScopeMap[targetNode.ScopeID] {
					continue
				}

				resultEdges = append(resultEdges, edge)

				if !visitedNodes[edge.ToID] {
					visitedNodes[edge.ToID] = true
					resultNodes = append(resultNodes, targetNode)
					nextQueue = append(nextQueue, edge.ToID)
				}
			}
		}
		queue = nextQueue
	}

	return resultNodes, resultEdges, nil
}
