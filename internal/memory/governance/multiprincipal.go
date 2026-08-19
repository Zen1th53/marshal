package governance

import (
	"context"
	"errors"
	"sync"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrUnauthorizedMemoryAccess   = errors.New("unauthorized memory access: principal cannot access record scope")
	ErrMemoryNotFoundOrRevoked    = errors.New("memory record not found or revoked/tombstoned")
)

type Principal struct {
	ID              string   `json:"id"`
	AllowedScopeIDs []string `json:"allowed_scope_ids"`
}

type MultiPrincipalGovernance struct {
	mu      sync.RWMutex
	records map[string]model.MemoryRecordV2
}

func NewMultiPrincipalGovernance() *MultiPrincipalGovernance {
	return &MultiPrincipalGovernance{
		records: make(map[string]model.MemoryRecordV2),
	}
}

func (g *MultiPrincipalGovernance) StoreRecord(rec model.MemoryRecordV2) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.records[rec.ID] = rec
}

func (g *MultiPrincipalGovernance) RevokeRecord(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if rec, ok := g.records[id]; ok {
		rec.Lifecycle = model.MemoryTombstoned
		g.records[id] = rec
	}
}

// GetMemoryByID provides strict direct-ID authorization, preventing unauthorized tenant enumeration or direct-guess attacks.
func (g *MultiPrincipalGovernance) GetMemoryByID(ctx context.Context, p Principal, id string) (model.MemoryRecordV2, error) {
	g.mu.RLock()
	rec, ok := g.records[id]
	g.mu.RUnlock()

	if !ok || rec.Lifecycle == model.MemoryTombstoned || rec.Lifecycle == model.MemoryRejected {
		return model.MemoryRecordV2{}, ErrMemoryNotFoundOrRevoked
	}

	// Scope authorization check
	authorized := false
	for _, allowed := range p.AllowedScopeIDs {
		if rec.ScopeID == allowed {
			authorized = true
			break
		}
	}

	if !authorized {
		return model.MemoryRecordV2{}, ErrUnauthorizedMemoryAccess
	}

	return rec, nil
}
