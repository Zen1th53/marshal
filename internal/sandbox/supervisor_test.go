//go:build linux && amd64

package sandbox

import (
	"encoding/binary"
	"golang.org/x/sys/unix"
	"testing"
)

func TestSocketFilterDeniesHostSocketsAndAlternateAPIs(t *testing.T) {
	for _, family := range []uint64{unix.AF_UNIX, unix.AF_NETLINK, unix.AF_PACKET, unix.AF_INET6} {
		if filterAction(unix.SYS_SOCKET, [6]uint64{family, unix.SOCK_STREAM}) != unix.SECCOMP_RET_USER_NOTIF {
			t.Fatalf("family %d bypasses observer", family)
		}
	}
	for _, nr := range []int{unix.SYS_CONNECT, unix.SYS_SENDTO, unix.SYS_SENDMSG, unix.SYS_SENDMMSG, unix.SYS_IO_URING_SETUP, unix.SYS_PTRACE, unix.SYS_PIDFD_GETFD} {
		if filterAction(nr, [6]uint64{}) != unix.SECCOMP_RET_USER_NOTIF {
			t.Fatalf("syscall %d bypasses observer", nr)
		}
	}
	if filterAction(unix.SYS_SOCKET, [6]uint64{unix.AF_INET, unix.SOCK_DGRAM}) != unix.SECCOMP_RET_USER_NOTIF {
		t.Fatal("raw UDP bypasses observer")
	}
	if filterAction(unix.SYS_SOCKET, [6]uint64{unix.AF_INET, unix.SOCK_STREAM}) != unix.SECCOMP_RET_ALLOW {
		t.Fatal("proxy TCP unavailable")
	}
}

// Evaluate the actual installed BPF, not a second policy implementation.
func filterAction(nr int, args [6]uint64) uint32 {
	var data [64]byte
	binary.LittleEndian.PutUint32(data[:4], uint32(nr))
	binary.LittleEndian.PutUint32(data[4:8], unix.AUDIT_ARCH_X86_64)
	for i, arg := range args {
		binary.LittleEndian.PutUint64(data[16+i*8:], arg)
	}
	var accumulator uint32
	for pc := 0; pc < len(socketFilter()); pc++ {
		ins := socketFilter()[pc]
		switch ins.Code {
		case unix.BPF_LD | unix.BPF_W | unix.BPF_ABS:
			accumulator = binary.LittleEndian.Uint32(data[ins.K:])
		case unix.BPF_ALU | unix.BPF_AND | unix.BPF_K:
			accumulator &= ins.K
		case unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K:
			if accumulator == ins.K {
				pc += int(ins.Jt)
			} else {
				pc += int(ins.Jf)
			}
		case unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K:
			if accumulator&ins.K != 0 {
				pc += int(ins.Jt)
			} else {
				pc += int(ins.Jf)
			}
		case unix.BPF_RET | unix.BPF_K:
			return ins.K
		default:
			panic("unsupported BPF instruction")
		}
	}
	panic("filter has no return")
}

func TestSocketFilterFastOpenAndCompatCannotBypass(t *testing.T) {
	if filterAction(unix.SYS_SENDMSG, [6]uint64{3, 0, unix.MSG_FASTOPEN}) != unix.SECCOMP_RET_USER_NOTIF {
		t.Fatal("TCP Fast Open bypass")
	}
	if filterAction(unix.SYS_SOCKET|0x40000000, [6]uint64{}) != unix.SECCOMP_RET_USER_NOTIF {
		t.Fatal("compat syscall bypass")
	}
}

func TestConnectedSendPreservesDestinationBoundary(t *testing.T) {
	if !connectedSend(notification{Arch: unix.AUDIT_ARCH_X86_64, Nr: unix.SYS_SENDTO}) {
		t.Fatal("connected proxy send refused")
	}
	for _, req := range []notification{
		{Arch: unix.AUDIT_ARCH_X86_64, Nr: unix.SYS_SENDTO, Args: [6]uint64{0, 0, 0, 0, 1}},
		{Arch: unix.AUDIT_ARCH_X86_64, Nr: unix.SYS_SENDTO, Args: [6]uint64{0, 0, 0, 0, 1 << 32}},
		{Arch: unix.AUDIT_ARCH_X86_64, Nr: unix.SYS_SENDTO, Args: [6]uint64{0, 0, 0, unix.MSG_FASTOPEN}},
		{Arch: unix.AUDIT_ARCH_I386, Nr: unix.SYS_SENDTO},
		{Arch: unix.AUDIT_ARCH_X86_64, Nr: unix.SYS_SENDMSG},
	} {
		if connectedSend(req) {
			t.Fatalf("destination boundary bypass: %+v", req)
		}
	}
}

func msghdr(name uint64, namelen uint32) []byte {
	b := make([]byte, 56)
	binary.LittleEndian.PutUint64(b, name)
	binary.LittleEndian.PutUint32(b[8:], namelen)
	return b
}

// Extreme decides sendmsg like connect and sendto: no name means the peer
// connect already confined, a name must be a local destination. Community
// refuses without reading the destination.
func TestMsgSendFollowsTheEditionDestinationBoundary(t *testing.T) {
	reads := 0
	reader := func(addr []byte) func(ptr, size uint64) []byte {
		return func(ptr, size uint64) []byte {
			reads++
			if ptr != 0x1000 || size != uint64(len(addr)) {
				t.Fatalf("read %#x/%d", ptr, size)
			}
			return addr
		}
	}
	none := reader(nil)
	for _, flags := range []uint64{0, unix.MSG_NOSIGNAL | unix.MSG_DONTWAIT} {
		if msgSendAllowed(msghdr(0, 0), flags, none) != editionIsExtreme {
			t.Fatalf("connected sendmsg flags %#x", flags)
		}
	}
	if reads != 0 {
		t.Fatal("connected sendmsg read a destination")
	}
	for _, addr := range [][]byte{inet4("127.0.0.1", 3000), inet6("::1", 8080), {afUnixFamily, 0, 0, 'x'}} {
		if msgSendAllowed(msghdr(0x1000, uint32(len(addr))), 0, reader(addr)) != editionIsExtreme {
			t.Fatalf("local sendmsg %v", addr)
		}
	}
	unixPath := append([]byte{afUnixFamily, 0}, "/tmp/dev.sock\x00"...)
	for _, addr := range [][]byte{inet4("192.0.2.1", 443), inet6("2001:db8::1", 443), unixPath, nil} {
		if msgSendAllowed(msghdr(0x1000, uint32(len(addr))), 0, reader(addr)) {
			t.Fatalf("outward sendmsg %v allowed", addr)
		}
	}
	if msgSendAllowed(msghdr(0, 0), unix.MSG_FASTOPEN, none) {
		t.Fatal("name-less Fast Open sendmsg allowed")
	}
	if msgSendAllowed(msghdr(0, 0)[:8], 0, none) || msgSendAllowed(nil, 0, none) {
		t.Fatal("short msghdr allowed")
	}
	if !editionIsExtreme && reads != 0 {
		t.Fatalf("Community read %d sendmsg destinations", reads)
	}
	if buildSupervisorAllowsMsgSend() != editionIsExtreme {
		t.Fatal("sendmsg edition gate")
	}
}

func TestDeniedNetlinkDiscoveryIsExactAndStillTrapped(t *testing.T) {
	allowed := notification{Arch: unix.AUDIT_ARCH_X86_64, Nr: unix.SYS_SOCKET, Args: [6]uint64{unix.AF_NETLINK, unix.SOCK_RAW | unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE}}
	if !deniedNetlinkDiscovery(allowed) {
		t.Fatal("exact route discovery not identified")
	}
	if filterAction(unix.SYS_SOCKET, allowed.Args) != unix.SECCOMP_RET_USER_NOTIF {
		t.Fatal("netlink socket bypasses kernel refusal")
	}
	for _, req := range []notification{
		{Arch: unix.AUDIT_ARCH_I386, Nr: unix.SYS_SOCKET, Args: allowed.Args},
		{Arch: unix.AUDIT_ARCH_X86_64, Nr: unix.SYS_CONNECT, Args: allowed.Args},
		{Arch: unix.AUDIT_ARCH_X86_64, Nr: unix.SYS_SOCKET, Args: [6]uint64{unix.AF_NETLINK, unix.SOCK_DGRAM, unix.NETLINK_ROUTE}},
		{Arch: unix.AUDIT_ARCH_X86_64, Nr: unix.SYS_SOCKET, Args: [6]uint64{unix.AF_NETLINK, unix.SOCK_RAW, unix.NETLINK_GENERIC}},
		{Arch: unix.AUDIT_ARCH_X86_64, Nr: unix.SYS_SOCKET, Args: [6]uint64{unix.AF_NETLINK, unix.SOCK_RAW | 0x10000, unix.NETLINK_ROUTE}},
		{Arch: unix.AUDIT_ARCH_X86_64, Nr: unix.SYS_SOCKET, Args: [6]uint64{unix.AF_UNIX, unix.SOCK_RAW, unix.NETLINK_ROUTE}},
	} {
		if deniedNetlinkDiscovery(req) {
			t.Fatalf("unexpected discovery exemption: %+v", req)
		}
	}
}
