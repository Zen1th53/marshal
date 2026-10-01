//go:build !linux

package api

import (
	"fmt"
	"net"
)

func kernelPeerUID(net.Conn) (uint32, error) {
	return 0, fmt.Errorf("kernel peer authentication unavailable")
}
