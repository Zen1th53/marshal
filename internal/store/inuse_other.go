//go:build !linux

package store

import "errors"

func processesHolding(paths ...string) ([]int, error) {
	return nil, errors.New("open-file inspection is supported on Linux only")
}

const inUseCheckSupported = false
