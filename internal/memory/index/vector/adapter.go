package vector

import (
	"context"
	"math"
	"sort"
	"sync"
)

type VectorSearchResult struct {
	MemoryID string  `json:"memory_id"`
	Score    float64 `json:"score"`
}

type VectorItem struct {
	MemoryID  string
	ProjectID string
	ScopeID   string
	Embedding []float32
}

type VectorBackend interface {
	Name() string
	Version() string
	UpsertVector(ctx context.Context, memoryID, projectID, scopeID string, embedding []float32) error
	DeleteVector(ctx context.Context, memoryID string) error
	SearchVectors(ctx context.Context, projectID string, allowedScopeIDs []string, queryEmbedding []float32, limit int) ([]VectorSearchResult, error)
	Rebuild(ctx context.Context, items []VectorItem) error
	Health(ctx context.Context) error
}

type LocalVectorStore struct {
	mu      sync.RWMutex
	vectors map[string]VectorItem
}

func NewLocalVectorStore() *LocalVectorStore {
	return &LocalVectorStore{
		vectors: make(map[string]VectorItem),
	}
}

func (s *LocalVectorStore) Name() string    { return "in-memory-cosine" }
func (s *LocalVectorStore) Version() string { return "1.0.0" }

func (s *LocalVectorStore) UpsertVector(ctx context.Context, memoryID, projectID, scopeID string, embedding []float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.vectors[memoryID] = VectorItem{
		MemoryID:  memoryID,
		ProjectID: projectID,
		ScopeID:   scopeID,
		Embedding: embedding,
	}
	return nil
}

func (s *LocalVectorStore) DeleteVector(ctx context.Context, memoryID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.vectors, memoryID)
	return nil
}

func (s *LocalVectorStore) Rebuild(ctx context.Context, items []VectorItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.vectors = make(map[string]VectorItem)
	for _, item := range items {
		s.vectors[item.MemoryID] = item
	}
	return nil
}

func (s *LocalVectorStore) Health(ctx context.Context) error {
	return nil
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0.0
	}
	var dot, normA, normB float64
	for i := 0; i < len(a); i++ {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0.0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func (s *LocalVectorStore) SearchVectors(ctx context.Context, projectID string, allowedScopeIDs []string, queryEmbedding []float32, limit int) ([]VectorSearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	allowedMap := make(map[string]bool)
	for _, sc := range allowedScopeIDs {
		allowedMap[sc] = true
	}

	var results []VectorSearchResult

	for _, item := range s.vectors {
		if item.ProjectID != projectID {
			continue
		}
		if len(allowedScopeIDs) > 0 && !allowedMap[item.ScopeID] {
			continue
		}

		score := cosineSimilarity(item.Embedding, queryEmbedding)
		results = append(results, VectorSearchResult{
			MemoryID: item.MemoryID,
			Score:    score,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}
