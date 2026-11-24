//go:build governed

package marshal

type concurrencySettings struct{}

func (s Settings) validateConcurrencySettings() error { return nil }

func EffectiveWorkerLimits(s Settings) (map[string]int, int) {
	cap := s.UltraConcurrency
	if cap == 0 {
		cap = DefaultSettings().UltraConcurrency
	}
	return nil, cap
}
