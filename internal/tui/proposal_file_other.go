//go:build !linux

package tui

import (
	"errors"
	"os"
)

func openProposalFile(root *os.Root, name string) (*os.File, error) {
	return nil, errors.New("proposal file transport unavailable on this platform")
}
