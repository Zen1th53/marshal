//go:build !governed && !linux

package processgroup

import (
	"fmt"
	"os/exec"
)

func WrapHeld(*exec.Cmd) (func() error, func(), error) {
	return nil, nil, fmt.Errorf("private cell held start requires Linux supervision")
}
