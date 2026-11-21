//go:build darwin

package workerterminal

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPTY opens a new pseudo-terminal pair the way posix_openpt, grantpt,
// unlockpt and ptsname do on macOS. The master's descriptor is returned for
// size ioctls. Both ends stay blocking descriptors outside the runtime
// poller, as the Linux master does.
func openPTY() (master *os.File, fd int, slave *os.File, err error) {
	fd, err = unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, -1, nil, err
	}
	master = os.NewFile(uintptr(fd), "worker-pty")
	name, err := darwinSlaveName(fd)
	if err != nil {
		master.Close()
		return nil, -1, nil, err
	}
	sfd, err := unix.Open(name, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		master.Close()
		return nil, -1, nil, err
	}
	return master, fd, os.NewFile(uintptr(sfd), name), nil
}

// darwinSlaveName grants and unlocks the slave, then reads its device path.
func darwinSlaveName(fd int) (string, error) {
	if err := ptyIoctl(fd, unix.TIOCPTYGRANT, 0); err != nil {
		return "", err
	}
	if err := ptyIoctl(fd, unix.TIOCPTYUNLK, 0); err != nil {
		return "", err
	}
	// TIOCPTYGNAME writes a NUL-terminated path into a 128-byte buffer.
	var name [128]byte
	if err := ptyIoctl(fd, unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))); err != nil {
		return "", err
	}
	return unix.ByteSliceToString(name[:]), nil
}

func ptyIoctl(fd int, req uint, arg uintptr) error {
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(req), arg); errno != 0 {
		return errno
	}
	return nil
}
