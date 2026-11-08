//go:build unix

package store

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// checkOwner refuses store directories owned by another user.
func checkOwner(fi fs.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("store: directory is owned by uid %d, not the current user", st.Uid)
	}
	return nil
}
