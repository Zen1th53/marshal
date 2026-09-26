//go:build !unix

package driver

import "os/exec"

// setProcessGroup leaves the default: without process groups, cancellation
// stops the worker process itself.
func setProcessGroup(*exec.Cmd) {}
