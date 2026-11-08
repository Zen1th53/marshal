//go:build !governed

package sandbox

import (
	"encoding/binary"
	"net"
)

// The confined worker runs in its own network namespace (bwrap
// --unshare-net): its loopback and abstract Unix sockets reach only what the
// task itself started, and the only route out is the run's recorded egress
// proxy, which in this edition grants every destination automatically. Within
// that namespace the private edition never refuses local sockets, so a dev
// server the task starts on localhost (IPv4 or IPv6) and abstract-socket IPC
// work. Outward traffic is still only possible, and recorded, through the
// proxy.

const (
	afUnix    = 1
	afInet    = 2
	afInet6   = 10
	afNetlink = 16
)

// buildSupervisorAllowsSocket lets a worker create Unix, IPv4, IPv6 and
// netlink sockets of any type inside its private namespace.
func buildSupervisorAllowsSocket(family uint64) bool {
	switch family {
	case afUnix, afInet, afInet6, afNetlink:
		return true
	}
	return false
}

// buildSupervisorAllowsConnect lets a worker connect to its own loopback and
// to abstract Unix sockets, which are scoped to its network namespace. A Unix
// socket path can be a bind-mounted host path (the worktree may itself live
// under /tmp), so a host relay around the recorded proxy cannot be told apart
// from the task's own socket: path-based Unix connects stay refused with
// evidence, as does any other destination (unreachable from the namespace).
func buildSupervisorAllowsConnect(addr []byte) bool {
	if len(addr) < 2 {
		return false
	}
	switch binary.LittleEndian.Uint16(addr[:2]) {
	case afUnix:
		return len(addr) > 3 && addr[2] == 0
	case afInet:
		return len(addr) >= 8 && net.IP(addr[4:8]).IsLoopback()
	case afInet6:
		return len(addr) >= 24 && net.IP(addr[8:24]).IsLoopback()
	}
	return false
}

// buildSupervisorAllowsMsgSend lets the supervisor decide a sendmsg by its
// destination instead of refusing it outright: runtimes such as Bun send
// through sendmsg on sockets connect already confined, and refusing each one
// floods the refusal record without protecting anything the namespace does not.
func buildSupervisorAllowsMsgSend() bool { return true }

// buildNoProxy keeps loopback requests inside the worker's namespace: with a
// proxy variable set and no exception, a client would send "localhost" through
// the host-side proxy and reach the host instead of the task's own server.
func buildNoProxy() string { return "localhost,127.0.0.1,::1" }
