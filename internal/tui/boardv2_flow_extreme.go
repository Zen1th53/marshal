//go:build !governed

package tui

import (
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
)

// laneLimits is the run's lane limit per provider and in total, as the
// dispatcher applies them (app.ProviderLaneLimits).
func laneLimits(s marshal.Settings) (map[string]int, int) {
	return app.ProviderLaneLimits(s)
}

// chatDecisions counts the build decisions handed to the Marshal chat that
// it has not answered: blocked ones wait for the owner's unsent chat text to
// be sent or cleared; the rest wait for the Marshal.
func (w *Workspace) chatDecisions() (blocked, waiting int) {
	w.permissions.mu.Lock()
	defer w.permissions.mu.Unlock()
	for _, p := range w.permissions.build.pending {
		if p == nil || p.answer != nil {
			continue
		}
		if p.blocked && !p.delivered {
			blocked++
		} else {
			waiting++
		}
	}
	return blocked, waiting
}
