//go:build governed

package workerenv

// HostFiltered reports whether Host filters this edition's worker
// environment.
const HostFiltered = false

// Host leaves the governed edition's worker environment as it was: its
// workers run behind the governed sandbox and credential broker, which own
// what reaches them. A nil env stays nil.
func Host(env []string) []string {
	return env
}
