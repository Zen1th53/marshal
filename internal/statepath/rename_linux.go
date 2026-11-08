//go:build linux

package statepath

import "golang.org/x/sys/unix"

func renameNamespace(old, target string) error {
	return unix.Renameat2(unix.AT_FDCWD, old, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE)
}
