package app

import (
	"github.com/Zen1th53/marshal/internal/marshal"
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
	// ramps paces new starts on a provider (or one model of it) after its
	// limit reset. They are kept apart from entries so a Clear after a
	// successful verification does not end a ramp half way.
	ramps map[string]recoveryRamp
}

// recoveryInterval is the least time between new starts on a provider that
// is coming back from its limit, until it reaches its normal cap.
const recoveryInterval = 5 * time.Second

// recoveryRamp is the pace of new starts after one reset: reset is the
// cooldown end that opened it, so a later limit opens a new one.
type recoveryRamp struct {
	reset  time.Time
	starts int
	last   time.Time
}

type providerHealthEntry struct {
	reason string
	until  time.Time
}

// providerHealth is process-wide: a provider quota belongs to the account,
// not to one run or runtime.
var providerHealth = &ProviderHealth{entries: map[string]providerHealthEntry{}}

// providerFallbackOrder is the one default fallback order for every role.
var providerFallbackOrder = marshal.DefaultProviderOrder()

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

// Reset forgets every cooldown and recovery ramp.
func (h *ProviderHealth) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = map[string]providerHealthEntry{}
	h.ramps = nil
}

// Recovering reports whether key is coming back from its limit at now: a
// cooldown ended and fewer than cap starts were paced since its reset.
func (h *ProviderHealth) Recovering(key string, now time.Time, cap int) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ramping := h.rampLocked(key, now, cap)
	return ramping
}

// rampLocked is key's current ramp at now: a cooldown that ended opens a
// new one for its reset; a ramp that reached cap is over.
func (h *ProviderHealth) rampLocked(key string, now time.Time, cap int) (recoveryRamp, bool) {
	r, ok := h.ramps[key]
	if e, has := h.entries[key]; has && !now.Before(e.until) && (!ok || !r.reset.Equal(e.until)) {
		r, ok = recoveryRamp{reset: e.until}, true
	}
	if !ok || r.starts >= cap {
		return recoveryRamp{}, false
	}
	return r, true
}

// RampAdmit reports whether a new start on key may begin at now. While key
// recovers from its limit it allows one start at once, then one per
// recoveryInterval until cap starts; retry is when the next may begin.
func (h *ProviderHealth) RampAdmit(key string, now time.Time, cap int) (retry time.Time, ok bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	r, ramping := h.rampLocked(key, now, cap)
	if !ramping || r.starts == 0 || !now.Before(r.last.Add(recoveryInterval)) {
		return time.Time{}, true
	}
	return r.last.Add(recoveryInterval), false
}

// RampTake records a start on key at now when it recovers, and returns the
// undo for a start that did not begin.
func (h *ProviderHealth) RampTake(key string, now time.Time, cap int) (undo func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ramping := h.rampLocked(key, now, cap)
	if !ramping {
		return func() {}
	}
	prev, had := h.ramps[key]
	if h.ramps == nil {
		h.ramps = map[string]recoveryRamp{}
	}
	r.starts++
	r.last = now
	// A finished ramp stays recorded so its reset does not open another.
	h.ramps[key] = r
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if had {
			h.ramps[key] = prev
		} else {
			delete(h.ramps, key)
		}
	}
}
