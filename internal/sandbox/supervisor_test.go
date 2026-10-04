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
