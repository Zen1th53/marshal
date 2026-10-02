//go:build linux

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
		cred, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		credErr = e
		if e == nil {
			uid = cred.Uid
		}
	}); err != nil {
		return 0, err
	}
	return uid, credErr
}
