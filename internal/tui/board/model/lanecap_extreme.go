//go:build !governed

package model

import "github.com/Zen1th53/marshal/internal/marshal"

// defaultGlobalCap is the extreme dispatcher's global lane cap when the run
// sets none.
var defaultGlobalCap = func() int { _, cap := marshal.EffectiveWorkerLimits(marshal.DefaultSettings()); return cap }()

// laneCap is the most lanes a run with these settings dispatches at once,
// as the extreme dispatcher computes it.
func laneCap(s marshal.Settings) int {
	_, cap := marshal.EffectiveWorkerLimits(s)
	return cap
}
