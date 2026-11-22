//go:build darwin || freebsd

package api

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// kernelPeerUID reads the connecting process's effective UID from the kernel
// (LOCAL_PEERCRED). macOS and FreeBSD have no SO_PEERCRED; without this the
// runtime socket refused every local client, so `marshal daemon` was unusable.
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
			uid = cred.Uid
		}
	}); err != nil {
		return 0, err
	}
	return uid, credErr
}
