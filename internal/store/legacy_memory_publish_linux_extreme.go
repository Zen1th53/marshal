//go:build !governed && linux

package store

import "golang.org/x/sys/unix"

func legacyPublishNamespace(stage, target string) error {
	return unix.Renameat2(unix.AT_FDCWD, stage, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE)
}
