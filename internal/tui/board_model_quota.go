package tui

import (
	"strings"

	"github.com/Zen1th53/marshal/internal/usage"
)

// providerModelLeft reports the percent left of the limits that apply to one
// model of a provider, from the last local reading, and whether it is known.
// For a provider with one limit per model family (agy) it is the family's
// most-constrained known window; for any other provider the same figure as
// providerLeft. A model no family covers is unknown, so the dispatcher keeps
// its provider-level rule for it.
func (w *Workspace) providerModelLeft(provider, model string) (int, bool) {
	qs, ok := w.usageQuotas()
	if !ok {
		return 0, false
	}
	return modelLeftIn(qs, provider, model)
}

// modelLeftIn is providerModelLeft on a reading.
func modelLeftIn(qs []usage.Quota, provider, model string) (int, bool) {
	key := quotaProviderKey(provider)
	for _, q := range qs {
		if q.Provider != key {
			continue
		}
		win, known := q.ModelBinding(model)
		if !known {
			return 0, false
		}
		return usageLeft(win), true
	}
	return 0, false
}

// quotaProviderKey is a worker or provider name as the limits reader names it.
func quotaProviderKey(provider string) string {
	key := strings.ToLower(strings.TrimSpace(provider))
	switch key {
	case "antigravity":
		return "agy"
	case "claude-code":
		return "claude"
	}
	return key
}
