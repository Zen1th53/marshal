//go:build !linux

package processgroup

import "os/exec"

// Descendant supervision currently requires Linux, like governed confinement.
func Wrap(*exec.Cmd) error     { return nil }
func Stop(cmd *exec.Cmd) error { return cmd.Process.Kill() }
