//go:build darwin

package api

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

func kernelPeerUID(conn net.Conn) (uint32, error) {
	c, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("Unix peer required")
	}
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var uid uint32
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, e := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		credErr = e
		if e == nil {
			if cred.Version != 0 {
				credErr = fmt.Errorf("unsupported kernel peer credential version: %d", cred.Version)
				return
			}
			uid = cred.Uid
		}
	}); err != nil {
		return 0, err
	}
	return uid, credErr
}
