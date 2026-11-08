//go:build !governed

package sandbox

// UnconfinedFallbackAllowed is true in the Marshal build: when confinement is
// unavailable or refuses, the work runs unconfined on the host and the
// evidence says so.
func UnconfinedFallbackAllowed() bool { return true }

// HostExecutionPreferred is true in the Marshal build: workers, required
// checks, and the verifier run directly on the host without bubblewrap or an
// egress proxy. Git worktrees isolate parallel tasks.
func HostExecutionPreferred() bool { return true }
