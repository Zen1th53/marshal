//go:build !governed

package app

import "github.com/Zen1th53/marshal/internal/marshal"

// WorkerConcurrency is how many workers each provider may run at once and
// the cap on all of them, as the dispatcher applies settings s.
func WorkerConcurrency(s marshal.Settings) (limits map[string]int, globalCap int) {
	return extremeProviderLimits(s)
}
