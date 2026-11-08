//go:build darwin || freebsd || netbsd

package fsread

import (
	"io/fs"
	"syscall"
)

func sysKey(fi fs.FileInfo) (StatKey, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return StatKey{}, false
	}
	return StatKey{CtimeNS: st.Ctimespec.Nano(), Ino: uint64(st.Ino), Dev: uint64(st.Dev)}, true
}
