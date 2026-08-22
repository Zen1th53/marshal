package tiering

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type StorageTier string

const (
	TierCorePinned      StorageTier = "CORE_PINNED"
	TierHotActive       StorageTier = "HOT_ACTIVE"
	TierWarmDurable     StorageTier = "WARM_DURABLE"
	TierColdHistorical  StorageTier = "COLD_HISTORICAL"
	TierArchivalEpisode StorageTier = "ARCHIVAL_EPISODE"
)

type TierEntry struct {
	Record       model.MemoryRecordV2 `json:"record"`
	Tier         StorageTier          `json:"tier"`
	LastAccessed time.Time            `json:"last_accessed"`
}

type TierManager struct {
	mu      sync.RWMutex
	entries map[string]TierEntry
}

func NewTierManager() *TierManager {
	return &TierManager{
		entries: make(map[string]TierEntry),
	}
}

func (m *TierManager) RegisterRecord(rec model.MemoryRecordV2, tier StorageTier, lastAccessed time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries[rec.ID] = TierEntry{
		Record:       rec,
		Tier:         tier,
		LastAccessed: lastAccessed,
	}
}

func (m *TierManager) GetRecordTier(id string) StorageTier {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.entries[id]
	if !ok {
		return ""
	}
	return entry.Tier
}

// RunMigrationSweep moves unpinned records across tiers based on age and activity.
func (m *TierManager) RunMigrationSweep(ctx context.Context, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, entry := range m.entries {
		if entry.Tier == TierCorePinned {
			continue // Pinned memory is immune to demotion
		}

		age := now.Sub(entry.LastAccessed)
		if age > 30*24*time.Hour {
			entry.Tier = TierColdHistorical
		} else if age > 7*24*time.Hour {
			entry.Tier = TierWarmDurable
		}
		m.entries[id] = entry
	}
}

// RecallProgressive retrieves a record regardless of its current storage tier.
func (m *TierManager) RecallProgressive(ctx context.Context, id string) (model.MemoryRecordV2, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.entries[id]
	if !ok {
		return model.MemoryRecordV2{}, errors.New("record not found")
	}
	return entry.Record, nil
}
