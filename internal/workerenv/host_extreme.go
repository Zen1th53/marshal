//go:build !governed

package workerenv

import "os"

// HostFiltered reports whether Host filters this edition's worker
// environment.
const HostFiltered = true

// Host is the environment a provider worker started on the host inherits
// from env (the process's own when env is nil): only what Safe keeps. The
// caller appends the launch's own variables (briefing, MCP wiring) after it.
func Host(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	return Safe(env, nil)
}
