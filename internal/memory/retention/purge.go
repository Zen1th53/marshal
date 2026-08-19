package retention

import (
	"context"
	"sync"

	"github.com/Zen1th53/marshal/internal/memory/index/graph"
	"github.com/Zen1th53/marshal/internal/memory/index/lexical"
	"github.com/Zen1th53/marshal/internal/memory/index/vector"
)

type PurgeConfig struct {
	Lexical *lexical.LexicalIndex
	Vector  vector.VectorBackend
	Graph   *graph.GraphIndex
}

type PurgeManager struct {
	config      PurgeConfig
	mu          sync.RWMutex
	purgeLedger map[string]bool // records permanently purged
}

func NewPurgeManager(config PurgeConfig) *PurgeManager {
	return &PurgeManager{
		config:      config,
		purgeLedger: make(map[string]bool),
	}
}

// HardPurge synchronously deletes a record from lexical, vector, and graph indexes and records it in the purge ledger.
func (p *PurgeManager) HardPurge(ctx context.Context, projectID, memoryID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.purgeLedger[memoryID] = true

	if p.config.Lexical != nil {
		_ = p.config.Lexical.DeleteRecord(ctx, memoryID)
	}

	if p.config.Vector != nil {
		_ = p.config.Vector.DeleteVector(ctx, memoryID)
	}

	if p.config.Graph != nil {
		_ = p.config.Graph.RemoveNode(ctx, memoryID)
	}

	return nil
}

// IsPurged checks whether a memory ID has been permanently purged.
func (p *PurgeManager) IsPurged(memoryID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.purgeLedger[memoryID]
}
