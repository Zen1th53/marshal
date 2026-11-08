//go:build !linux || !amd64

package sandbox

import (
	"context"
	"fmt"
	"os/exec"
)

const supervisorPath = "/run/marshal-supervisor"

type Refusal struct {
	Host      string
	Port      int
	Operation string
	// LocalKernelDiscovery is never set off Linux amd64; it keeps callers
	// portable.
	LocalKernelDiscovery bool
}

func SupervisedArgv([]string) ([]string, string, error) {
	return nil, "", fmt.Errorf("socket confinement requires Linux amd64")
}
func AttachSupervisor(context.Context, *exec.Cmd, string, func(context.Context, Refusal) error) (func() error, error) {
	return nil, fmt.Errorf("socket confinement unavailable")
}
