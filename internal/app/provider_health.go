package app

import (
	"strings"
	"sync"
	"time"
)

// ProviderHealth is the single in-memory registry of providers that reported
// their own quota or rate limit. Worker failover, dispatch admission, plan
// reassignment and review/verify role selection all read and write it.
// SQLite persistence is a later step (M3).
type ProviderHealth struct {
	mu      sync.RWMutex
	entries map[string]providerHealthEntry
}

type providerHealthEntry struct {
	reason string
	until  time.Time
}

// providerHealth is process-wide: a provider quota belongs to the account,
// not to one run or runtime.
var providerHealth = &ProviderHealth{entries: map[string]providerHealthEntry{}}

// providerFallbackOrder is the one default fallback order for every role.
var providerFallbackOrder = []string{"codex", "claude", "agy", "opencode"}

func providerHealthKey(provider string) string {
	name := strings.ToLower(strings.TrimSpace(provider))
	switch name {
	case "antigravity":
		return "agy"
	case "claude-code":
		return "claude"
	}
	return name
}

// Mark records provider as cooling down until until; a later existing reset wins.
func (h *ProviderHealth) Mark(provider, reason string, until time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	key := providerHealthKey(provider)
	if existing, ok := h.entries[key]; ok && existing.until.After(until) {
		until = existing.until
	}
	h.entries[key] = providerHealthEntry{reason: reason, until: until}
}

// CooldownUntil returns the recorded reset time and reason, expired or not.
func (h *ProviderHealth) CooldownUntil(provider string) (time.Time, string, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	e, ok := h.entries[providerHealthKey(provider)]
	return e.until, e.reason, ok
}

// Cooling reports whether provider is still cooling down at now.
func (h *ProviderHealth) Cooling(provider string, now time.Time) (string, bool) {
	until, reason, ok := h.CooldownUntil(provider)
	if !ok || !now.Before(until) {
		return "", false
	}
	return reason, true
}

// Clear forgets provider's cooldown (for example after a successful verification).
func (h *ProviderHealth) Clear(provider string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.entries, providerHealthKey(provider))
}

// Reset forgets every cooldown.
func (h *ProviderHealth) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = map[string]providerHealthEntry{}
}
