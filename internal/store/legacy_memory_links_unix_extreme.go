//go:build !governed && (linux || darwin || freebsd || openbsd || netbsd || dragonfly)

package store

import (
	"os"
	"syscall"
)

func legacyMultipleLinks(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && stat.Nlink > 1
}
