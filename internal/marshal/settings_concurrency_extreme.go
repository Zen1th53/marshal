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
