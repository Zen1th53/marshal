package execution

import (
	"os"

	"github.com/Zen1th53/marshal/internal/workerenv"
)

// ComputeSafeEnvironment builds a minimal, scrubbed environment for child
// processes: the entries of inherited (the process's own when nil) that
// workerenv allows a host worker, any extraAllowed names, and the task's
// scoped secrets. The same allowlist filters the live host worker launches
// (workerenv.Host).
func ComputeSafeEnvironment(inherited []string, extraAllowed []string, taskSecrets map[string]string) []string {
	if inherited == nil {
		inherited = os.Environ()
	}
	safeEnv := workerenv.Safe(inherited, extraAllowed)
	for k, v := range taskSecrets {
		if k != "" && v != "" {
			safeEnv = append(safeEnv, k+"="+v)
		}
	}
	return safeEnv
}
