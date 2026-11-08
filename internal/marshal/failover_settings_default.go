//go:build governed

package marshal

type failoverSettings struct{}
type failoverTask struct{}

func (s Settings) validateFailoverSettings() error { return nil }
