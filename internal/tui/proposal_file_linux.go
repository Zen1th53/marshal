//go:build linux

package tui

import (
	"golang.org/x/sys/unix"
	"os"
)

func openProposalFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
}
