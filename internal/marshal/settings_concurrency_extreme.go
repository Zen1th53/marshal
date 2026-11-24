//go:build !governed

package marshal

import "errors"

// ProviderLimits captures per-provider worker concurrency limits.
type ProviderLimits struct {
	Codex    int `json:"codex,omitempty"`
	Claude   int `json:"claude,omitempty"`
	Agy      int `json:"agy,omitempty"`
	OpenCode int `json:"opencode,omitempty"`
}

type concurrencySettings struct {
	ProviderLimits ProviderLimits `json:",omitempty"`
	GlobalCap      int            `json:",omitempty"`
}

func (s Settings) validateConcurrencySettings() error {
	if s.GlobalCap < 0 || s.ProviderLimits.Codex < 0 || s.ProviderLimits.Claude < 0 || s.ProviderLimits.Agy < 0 || s.ProviderLimits.OpenCode < 0 {
		return errors.New("invalid settings limit")
	}
	return nil
}

// EffectiveWorkerLimits is the dispatch capacity, including unconfigured defaults.
func EffectiveWorkerLimits(s Settings) (map[string]int, int) {
	limits := map[string]int{"codex": 4, "claude": 2, "agy": 4, "opencode": 4}
	for p, n := range map[string]int{"codex": s.ProviderLimits.Codex, "claude": s.ProviderLimits.Claude, "agy": s.ProviderLimits.Agy, "opencode": s.ProviderLimits.OpenCode} {
		if n > 0 {
			limits[p] = n
		}
	}
	cap := s.GlobalCap
	if cap == 0 {
		cap = 12
	}
	return limits, cap
}
