package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type Config struct {
	MaxEntries int
	TTL        time.Duration
}

type QueryKey struct {
	ProjectID string
	ScopeIDs  []string
	Query     string
	TopK      int
	AsOf      *time.Time
}

func (k QueryKey) Hash() string {
	h := sha256.New()
	scopes := append([]string(nil), k.ScopeIDs...)
	sort.Strings(scopes)
	fmt.Fprintf(h, "%s:%v:%s:%d:", k.ProjectID, scopes, k.Query, k.TopK)
	if k.AsOf != nil {
		fmt.Fprintf(h, "%d", k.AsOf.UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil))
}

type cacheEntry struct {
	projectID string
	scopeIDs  []string
	results   []model.MemoryRecordV2
	expiresAt time.Time
}

type BoundedCache struct {
	mu      sync.RWMutex
	config  Config
	entries map[string]cacheEntry
}

func NewBoundedCache(cfg Config) *BoundedCache {
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 500
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 5 * time.Minute
	}
	return &BoundedCache{
		config:  cfg,
		entries: make(map[string]cacheEntry),
	}
}

// Get retrieves cached memory records if not expired.
func (c *BoundedCache) Get(k QueryKey) ([]model.MemoryRecordV2, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	hash := k.Hash()
	entry, ok := c.entries[hash]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.results, true
}

// Put stores query results bounded by capacity.
func (c *BoundedCache) Put(k QueryKey, results []model.MemoryRecordV2) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Simple eviction if exceeding capacity
	if len(c.entries) >= c.config.MaxEntries {
		for h := range c.entries {
			delete(c.entries, h)
			break
		}
	}

	hash := k.Hash()
	c.entries[hash] = cacheEntry{
		projectID: k.ProjectID,
		scopeIDs:  k.ScopeIDs,
		results:   results,
		expiresAt: time.Now().Add(c.config.TTL),
	}
}

// InvalidateScope evicts all cached queries touching the specified project or scope.
func (c *BoundedCache) InvalidateScope(scopeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for h, entry := range c.entries {
		if entry.projectID == scopeID {
			delete(c.entries, h)
			continue
		}
		for _, s := range entry.scopeIDs {
			if s == scopeID {
				delete(c.entries, h)
				break
			}
		}
	}
}

// Purge completely clears the cache.
func (c *BoundedCache) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]cacheEntry)
}
