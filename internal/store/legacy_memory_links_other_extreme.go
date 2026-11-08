//go:build !governed && !(linux || darwin || freebsd || openbsd || netbsd || dragonfly)

package store

import "os"

func legacyMultipleLinks(info os.FileInfo) bool { return false }
