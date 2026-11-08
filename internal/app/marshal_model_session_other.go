//go:build !unix

package app

import "os/exec"

func detachControllingTerminal(*exec.Cmd) {}

// killProviderGroupOnCancel keeps exec's default kill of the provider.
func killProviderGroupOnCancel(*exec.Cmd) {}
