//go:build linux && amd64

package sandbox

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const supervisorArg = "__marshal_socket_supervisor"
const supervisorPath = "/run/marshal-supervisor"

type Refusal struct {
	Host      string
	Port      int
	Operation string
}

// The trampoline runs on a locked thread and execs from that same thread:
// seccomp is inherited by the executable and every subsequent descendant.
func init() {
	if len(os.Args) > 3 && os.Args[1] == supervisorArg {
		runtime.LockOSThread()
		if err := enterSupervisor(os.Args[2], os.Args[3:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(125)
		}
		os.Exit(125)
	}
}

func socketFilter() []unix.SockFilter {
	var f []unix.SockFilter
	stmt := func(code uint16, k uint32) { f = append(f, unix.SockFilter{Code: code, K: k}) }
	jump := func(k uint32, yes, no uint8) {
		f = append(f, unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: k, Jt: yes, Jf: no})
	}
	stmt(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS, 4)
	jump(unix.AUDIT_ARCH_X86_64, 1, 0)
	stmt(unix.BPF_RET|unix.BPF_K, unix.SECCOMP_RET_USER_NOTIF)
	stmt(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS, 0)
	f = append(f, unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K, K: 0x40000000, Jf: 1})
	stmt(unix.BPF_RET|unix.BPF_K, unix.SECCOMP_RET_USER_NOTIF)
	jump(unix.SYS_SOCKET, 0, 7)
	stmt(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS, 16)
	jump(unix.AF_INET, 0, 4)
	stmt(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS, 24)
	stmt(unix.BPF_ALU|unix.BPF_AND|unix.BPF_K, 15)
	jump(unix.SOCK_STREAM, 0, 1)
	stmt(unix.BPF_RET|unix.BPF_K, unix.SECCOMP_RET_ALLOW)
	stmt(unix.BPF_RET|unix.BPF_K, unix.SECCOMP_RET_USER_NOTIF)
	jump(unix.SYS_SENDMSG, 0, 6)
	stmt(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS, 16)
	jump(3, 0, 3)
	stmt(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS, 32)
	f = append(f, unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K, K: unix.MSG_FASTOPEN, Jt: 1})
	stmt(unix.BPF_RET|unix.BPF_K, unix.SECCOMP_RET_ALLOW)
	stmt(unix.BPF_RET|unix.BPF_K, unix.SECCOMP_RET_USER_NOTIF)
	for _, nr := range []int{unix.SYS_CONNECT, unix.SYS_SENDTO, unix.SYS_SENDMMSG, unix.SYS_IO_URING_SETUP, unix.SYS_PTRACE, unix.SYS_PROCESS_VM_WRITEV, unix.SYS_PIDFD_GETFD, unix.SYS_SETNS, unix.SYS_UNSHARE, unix.SYS_MOUNT, unix.SYS_OPEN_BY_HANDLE_AT} {
		jump(uint32(nr), 0, 1)
		stmt(unix.BPF_RET|unix.BPF_K, unix.SECCOMP_RET_USER_NOTIF)
	}
	stmt(unix.BPF_RET|unix.BPF_K, unix.SECCOMP_RET_ALLOW)
	return f
}

func enterSupervisor(socketPath string, argv []string) error {
	socket, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	if err := unix.Connect(socket, &unix.SockaddrUnix{Name: socketPath}); err != nil {
		return err
	}
	if socket != 3 {
		if err := unix.Dup3(socket, 3, unix.O_CLOEXEC); err != nil {
			return err
		}
		unix.Close(socket)
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	f := socketFilter()
	prog := unix.SockFprog{Len: uint16(len(f)), Filter: &f[0]}
	fd, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, unix.SECCOMP_FILTER_FLAG_NEW_LISTENER, uintptr(unsafe.Pointer(&prog)))
	if errno != 0 {
		return fmt.Errorf("socket confinement unavailable: %w", errno)
	}
	if err := unix.Sendmsg(3, []byte{1}, unix.UnixRights(int(fd)), nil, 0); err != nil {
		return err
	}
	unix.Close(int(fd))
	unix.Close(3)
	// No inherited supervisor descriptor or authority reaches worker code.
	return unix.Exec(argv[0], argv, os.Environ())
}

func SupervisedArgv(argv []string) ([]string, string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, "", err
	}
	return append([]string{supervisorPath, supervisorArg, "/run/marshal-observer.sock"}, argv...), exe, nil
}

// These structs are the fixed Linux seccomp notification ABI.
type notification struct {
	ID    uint64
	PID   uint32
	Flags uint32
	Nr    int32
	Arch  uint32
	IP    uint64
	Args  [6]uint64
}
type response struct {
	ID    uint64
	Val   int64
	Error int32
	Flags uint32
}

func ioctl(fd int, op uint, ptr unsafe.Pointer) error {
	_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(op), uintptr(ptr))
	if e != 0 {
		return e
	}
	return nil
}

// AttachSupervisor must be called before Start. All errors terminate the
// governed process; no failed observer is allowed to leave a worker running.
func AttachSupervisor(ctx context.Context, cmd *exec.Cmd, socketPath string, observe func(context.Context, Refusal) error) (func() error, error) {
	if observe == nil {
		return nil, errors.New("socket refusal observer required")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	return func() error {
		defer listener.Close()
		var conn *net.UnixConn
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			listener.SetDeadline(time.Now().Add(100 * time.Millisecond))
			conn, err = listener.AcceptUnix()
			if err == nil {
				break
			}
			if e, ok := err.(net.Error); !ok || !e.Timeout() {
				return err
			}
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var data [1]byte
		control := make([]byte, unix.CmsgSpace(4))
		_, n, _, _, err := conn.ReadMsgUnix(data[:], control)
		if err != nil {
			return err
		}
		msgs, err := unix.ParseSocketControlMessage(control[:n])
		if err != nil || len(msgs) != 1 {
			return errors.New("missing seccomp observer")
		}
		rights, err := unix.ParseUnixRights(&msgs[0])
		if err != nil || len(rights) != 1 {
			return errors.New("invalid seccomp observer")
		}
		fd := rights[0]
		defer unix.Close(fd)
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			p := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			if _, err := unix.Poll(p, 100); err != nil && err != unix.EINTR {
				return err
			}
			if p[0].Revents&unix.POLLIN == 0 {
				continue
			}
			var req notification
			if err := ioctl(fd, unix.SECCOMP_IOCTL_NOTIF_RECV, unsafe.Pointer(&req)); err != nil {
				if err == unix.EINTR || err == unix.ENOENT {
					continue
				}
				return err
			}
			refusal := Refusal{Host: "socket-operation", Operation: strconv.Itoa(int(req.Nr))}
			allow := connectedSend(req)
			if req.Arch == unix.AUDIT_ARCH_X86_64 && req.Nr == unix.SYS_CONNECT {
				var addr [128]byte
				size := req.Args[2]
				if size > uint64(len(addr)) {
					size = uint64(len(addr))
				}
				local := []unix.Iovec{{Base: &addr[0], Len: size}}
				remote := []unix.RemoteIovec{{Base: uintptr(req.Args[1]), Len: int(size)}}
				if n, err := unix.ProcessVMReadv(int(req.PID), local, remote, 0); err == nil && n >= 8 && binary.LittleEndian.Uint16(addr[:2]) == unix.AF_INET {
					refusal.Host = net.IP(addr[4:8]).String()
					refusal.Port = int(binary.BigEndian.Uint16(addr[2:4]))
					allow = refusal.Host == "127.0.0.1" && refusal.Port == 18080
				}
			} else if req.Arch == unix.AUDIT_ARCH_X86_64 && req.Nr == unix.SYS_SOCKET {
				refusal.Host = fmt.Sprintf("socket-family-%d-type-%d", req.Args[0], req.Args[1]&15)
			}
			resp := response{ID: req.ID, Error: -int32(unix.EACCES)}
			if allow {
				resp.Error = 0
				resp.Flags = unix.SECCOMP_USER_NOTIF_FLAG_CONTINUE
			} else {
				if err := observe(ctx, refusal); err != nil {
					return fmt.Errorf("refusal delivery failed: %w", err)
				}
			}
			if err := ioctl(fd, unix.SECCOMP_IOCTL_NOTIF_SEND, unsafe.Pointer(&resp)); err != nil && err != unix.ENOENT {
				return err
			}
		}
	}, nil
}

// A destination-free send cannot open a connection. Workers inherit no socket
// descriptors and can create only TCP streams; connect confines those streams
// to the proxy. Explicit destinations and Fast Open still require refusal.
func connectedSend(req notification) bool {
	return req.Arch == unix.AUDIT_ARCH_X86_64 && req.Nr == unix.SYS_SENDTO && req.Args[4] == 0 && req.Args[3]&unix.MSG_FASTOPEN == 0
}
