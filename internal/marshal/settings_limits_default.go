//go:build governed

package marshal

type runtimeLimitSettings struct{}

func (s Settings) validateRuntimeLimitSettings() error { return nil }
