//go:build governed

package model

import "github.com/Zen1th53/marshal/internal/marshal"

// laneCap is the most lanes a run with these settings dispatches at once:
// the default edition's dispatcher holds ULTRA runs to UltraConcurrency.
func laneCap(s marshal.Settings) int {
	if s.UltraConcurrency > 0 {
		return s.UltraConcurrency
	}
	return marshal.DefaultSettings().UltraConcurrency
}
