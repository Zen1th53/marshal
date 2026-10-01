//go:build linux

package store

import (
	"os"
	"path/filepath"
	"strconv"
)

// processesHolding lists the processes (including this one) that have any of
// the paths open, by reading /proc/<pid>/fd links. Processes of other users
// cannot be inspected and are not reported; MARSHAL state is owner-only, so
// only the owner's processes can open it in the first place.
func processesHolding(paths ...string) ([]int, error) {
	want := map[string]bool{}
	for _, p := range paths {
		if abs, err := filepath.Abs(p); err == nil {
			want[abs] = true
		}
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var holders []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", entry.Name(), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join("/proc", entry.Name(), "fd", fd.Name()))
			if err == nil && want[target] {
				holders = append(holders, pid)
				break
			}
		}
	}
	return holders, nil
}

const inUseCheckSupported = true
