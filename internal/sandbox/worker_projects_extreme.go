//go:build !governed && unix

package sandbox

import (
	"fmt"
	"github.com/Zen1th53/marshal/internal/model"
)

// workerProjectCacheMounts returns the late AGY project cache mount. The 16 MiB
// size bound needs bubblewrap --size; on a bubblewrap without it the mount is
// still made (the worker must not be blocked) but its size is unbounded by
// this option, and the second result is non-empty evidence for the isolation
// reason.
func workerProjectCacheMounts(request model.SandboxRequest, sizeBounded bool) ([]string, string, error) {
	if request.WorkerMCPMask.Empty() {
		return nil, "", nil
	}
	if !request.WorkerMCPMask.ValidEnvelope(request) {
		return nil, "", fmt.Errorf("%w: stale AGY project cache envelope", model.ErrInvalid)
	}
	target := request.WorkerMCPMask.ProjectCacheTarget(request.Worktree)
	if target == "" {
		return nil, "", fmt.Errorf("%w: stale AGY project cache identity", model.ErrInvalid)
	}
	// The readonly generated parent is already mounted. This exact late child
	// mount preserves MCP immutability and cannot mutate a host directory.
	if !sizeBounded {
		return []string{"--tmpfs", target}, "AGY project cache tmpfs size unbounded: bubblewrap lacks --size", nil
	}
	return []string{"--size", "16777216", "--tmpfs", target}, "", nil
}
