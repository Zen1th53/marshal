//go:build !unix

package app

import "os/exec"

func detachControllingTerminal(*exec.Cmd) {}
