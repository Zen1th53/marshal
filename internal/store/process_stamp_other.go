//go:build !linux && !darwin

package store

import "fmt"

func supervisorProcessStamp(pid int) (string, error) {
	return "", fmt.Errorf("process identity unavailable on this platform")
}
