//go:build !linux

package processgroup

import (
	"fmt"
	"os/exec"
)

type Reference struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
}

func Identify(cmd *exec.Cmd) (Reference, error) { return Reference{}, nil }
func StopReference(ref Reference) error {
	return fmt.Errorf("recovered worker supervision unavailable")
}
