package sqlitevec

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/Zen1th53/marshal/internal/memory/index/vector"
)

var (
	ErrVersionMismatch = errors.New("sqlite-vec backend version mismatch")
)

type Config struct {
	ExpectedVersion string
}

type Backend struct {
	mu      sync.RWMutex
	config  Config
	version string
	vectors map[string]vector.VectorItem
}

func NewBackend(config Config) *Backend {
	ver := "0.1.6"
	return &Backend{
		config:  config,
		version: ver,
		vectors: make(map[string]vector.VectorItem),
	}
}

func (b *Backend) Name() string    { return "sqlite-vec-local" }
func (b *Backend) Version() string { return b.version }

func (b *Backend) Health(ctx context.Context) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.config.ExpectedVersion != "" && b.config.ExpectedVersion != b.version {
		return fmt.Errorf("%w: expected %s, found %s", ErrVersionMismatch, b.config.ExpectedVersion, b.version)
	}
	return nil
}

func (b *Backend) UpsertVector(ctx context.Context, memoryID, projectID, scopeID string, embedding []float32) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.vectors[memoryID] = vector.VectorItem{
		MemoryID:  memoryID,
		ProjectID: projectID,
		ScopeID:   scopeID,
		Embedding: embedding,
	}
	return nil
}

func (b *Backend) DeleteVector(ctx context.Context, memoryID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.vectors, memoryID)
	return nil
}

func (b *Backend) Rebuild(ctx context.Context, items []vector.VectorItem) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.vectors = make(map[string]vector.VectorItem)
	for _, it := range items {
		b.vectors[it.MemoryID] = it
	}
	return nil
}

func cosineSim(a, b []float32) float64 {
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

func (b *Backend) SearchVectors(ctx context.Context, projectID string, allowedScopeIDs []string, queryEmbedding []float32, limit int) ([]vector.VectorSearchResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	allowedMap := make(map[string]bool)
	for _, sc := range allowedScopeIDs {
		allowedMap[sc] = true
	}

	var results []vector.VectorSearchResult

	for _, item := range b.vectors {
		if item.ProjectID != projectID {
			continue
		}
		// Strict partition/scope check before disclosing any similarity
		if len(allowedScopeIDs) > 0 && !allowedMap[item.ScopeID] {
			continue
		}

		score := cosineSim(item.Embedding, queryEmbedding)
		results = append(results, vector.VectorSearchResult{
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
