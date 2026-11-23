//go:build !linux || governed

package driver

import (
	"fmt"
	"os/exec"
)

// Without the private supervisor's barrier there is no way to hold a worker
// before it starts, so supervised launch is unavailable here.
func wrapHeld(*exec.Cmd) (func() error, func(), error) {
	return nil, nil, fmt.Errorf("%w: no startup barrier on this platform or edition", ErrLaunchUnsupported)
}

func heldIdentity(*exec.Cmd) (ProcessIdentity, error) {
	return ProcessIdentity{}, fmt.Errorf("%w: no process identity on this platform or edition", ErrLaunchUnsupported)
}

// HostProcesses reports that this host cannot verify worker processes.
func HostProcesses() ProcessAdapter { return unsupportedProcesses{} }

type unsupportedProcesses struct{}

func (unsupportedProcesses) BootID() (string, error) {
	return "", fmt.Errorf("%w: no boot identity on this platform or edition", ErrLaunchUnsupported)
}
func (unsupportedProcesses) Find(string) ([]ProcessIdentity, error) {
	return nil, ErrLaunchUnsupported
}
func (unsupportedProcesses) Alive(ProcessIdentity) (bool, error) { return false, ErrLaunchUnsupported }
func (unsupportedProcesses) Stop(ProcessIdentity) error          { return ErrLaunchUnsupported }
