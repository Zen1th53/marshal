//go:build governed

package sandbox

// UnconfinedFallbackAllowed is false in the default build: every confinement
// refusal stays fail-closed.
func UnconfinedFallbackAllowed() bool { return false }

// HostExecutionPreferred is false in the default build: checks and the
// verifier run confined.
func HostExecutionPreferred() bool { return false }
