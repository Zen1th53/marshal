//go:build !governed

package app

import "github.com/Zen1th53/marshal/internal/marshal"

// ProviderLaneLimits is how many lanes the dispatcher opens per provider
// for a run with these settings, and its global cap: the same numbers
// dispatch holds the run to, for the Board to show.
func ProviderLaneLimits(s marshal.Settings) (map[string]int, int) {
	return extremeProviderLimits(s)
}
