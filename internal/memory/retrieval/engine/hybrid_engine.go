package engine

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/index/graph"
	"github.com/Zen1th53/marshal/internal/memory/index/lexical"
	"github.com/Zen1th53/marshal/internal/memory/index/vector"
)

type Config struct {
	Lexical *lexical.LexicalIndex
	Vector  vector.VectorBackend
	Graph   *graph.GraphIndex
	Timeout time.Duration
}

type QueryParams struct {
	ProjectID       string    `json:"project_id"`
	Query           string    `json:"query"`
	QueryEmbedding  []float32 `json:"query_embedding,omitempty"`
	AllowedScopeIDs []string  `json:"allowed_scope_ids"`
	Limit           int       `json:"limit"`
}

type CandidateMatch struct {
	MemoryID string  `json:"memory_id"`
	Channel  string  `json:"channel"`
	Score    float64 `json:"score"`
}

type QueryResult struct {
	Candidates       []CandidateMatch `json:"candidates"`
	DegradedChannels []string         `json:"degraded_channels,omitempty"`
}

type HybridEngine struct {
	config Config
}

func NewHybridEngine(config Config) *HybridEngine {
	if config.Timeout == 0 {
		config.Timeout = 100 * time.Millisecond
	}
	return &HybridEngine{config: config}
}

// Query executes lexical, dense vector, and graph searches in parallel with per-channel timeouts and graceful degradation.
func (h *HybridEngine) Query(ctx context.Context, params QueryParams) (QueryResult, error) {
	if err := ctx.Err(); err != nil {
		return QueryResult{}, err
	}

	var mu sync.Mutex
	var candidates []CandidateMatch
	var degraded []string

	var wg sync.WaitGroup

	// 1. Lexical Channel
	if h.config.Lexical != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lexCtx, cancel := context.WithTimeout(ctx, h.config.Timeout)
			defer cancel()

			res, err := h.config.Lexical.Search(lexCtx, params.ProjectID, params.Query, params.Limit)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				degraded = append(degraded, "lexical")
				return
			}
			for _, r := range res {
				candidates = append(candidates, CandidateMatch{
					MemoryID: r.MemoryID,
					Channel:  "lexical",
					Score:    r.Score,
				})
			}
		}()
	}

	// 2. Vector Channel
	if h.config.Vector != nil && len(params.QueryEmbedding) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vecCtx, cancel := context.WithTimeout(ctx, h.config.Timeout)
			defer cancel()

			res, err := h.config.Vector.SearchVectors(vecCtx, params.ProjectID, params.AllowedScopeIDs, params.QueryEmbedding, params.Limit)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				degraded = append(degraded, "vector")
				return
			}
			for _, r := range res {
				candidates = append(candidates, CandidateMatch{
					MemoryID: r.MemoryID,
					Channel:  "vector",
					Score:    r.Score,
				})
			}
		}()
	}

	// 3. Graph Channel
	if h.config.Graph != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			graphCtx, cancel := context.WithTimeout(ctx, h.config.Timeout)
			defer cancel()

			// Traversal from seed query text/symbols
			nodes, _, err := h.config.Graph.Traverse(graphCtx, []string{params.Query}, params.AllowedScopeIDs, time.Now().UTC(), 2)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				degraded = append(degraded, "graph")
				return
			}
			for _, n := range nodes {
				candidates = append(candidates, CandidateMatch{
					MemoryID: n.ID,
					Channel:  "graph",
					Score:    1.0,
				})
			}
		}()
	}

	wg.Wait()

	if err := ctx.Err(); err != nil {
		return QueryResult{}, err
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	if params.Limit > 0 && len(candidates) > params.Limit {
		candidates = candidates[:params.Limit]
	}

	return QueryResult{
		Candidates:       candidates,
		DegradedChannels: degraded,
	}, nil
}
