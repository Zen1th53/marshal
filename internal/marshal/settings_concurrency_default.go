//go:build governed

package marshal

type concurrencySettings struct{}

func (s Settings) validateConcurrencySettings() error { return nil }
