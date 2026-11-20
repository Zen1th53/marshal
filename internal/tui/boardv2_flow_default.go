//go:build governed

package tui

import "github.com/Zen1th53/marshal/internal/marshal"

// laneLimits is the run's lane limit: the default edition holds a run to
// UltraConcurrency in total and sets no per-provider limit.
func laneLimits(s marshal.Settings) (map[string]int, int) {
	if s.UltraConcurrency > 0 {
		return nil, s.UltraConcurrency
	}
	return nil, marshal.DefaultSettings().UltraConcurrency
}

// chatDecisions counts build decisions waiting on the Marshal chat; the
// default edition hands none to it.
func (w *Workspace) chatDecisions() (blocked, waiting int) { return 0, 0 }
