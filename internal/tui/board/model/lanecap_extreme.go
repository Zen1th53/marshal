//go:build !governed

package model

import "github.com/Zen1th53/marshal/internal/marshal"

// defaultGlobalCap is the extreme dispatcher's global lane cap when the run
// sets none.
const defaultGlobalCap = 12

// laneCap is the most lanes a run with these settings dispatches at once,
// as the extreme dispatcher computes it.
func laneCap(s marshal.Settings) int {
	if s.GlobalCap > 0 {
		return s.GlobalCap
	}
	return defaultGlobalCap
}
