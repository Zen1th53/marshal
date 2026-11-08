//go:build unix

package project

import (
	"fmt"
	"os"
	"syscall"
)

// ensureShortSocketDir creates the fallback socket directory 0700 and refuses
// one that is a symlink, belongs to another user or is group/world accessible.
func ensureShortSocketDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create socket directory %s: %w", dir, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect socket directory: %w", err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || st.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("socket directory %s is not a directory owned by the current user", dir)
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("secure socket directory %s: %w", dir, err)
		}
	}
	return nil
}
