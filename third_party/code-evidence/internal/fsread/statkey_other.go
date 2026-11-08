//go:build !linux && !darwin && !freebsd && !netbsd

package fsread

import "io/fs"

// sysKey is unavailable here: stat-based caching is disabled.
func sysKey(fs.FileInfo) (StatKey, bool) { return StatKey{}, false }
