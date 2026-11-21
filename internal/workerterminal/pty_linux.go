//go:build linux

package workerterminal

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// openPTY opens a new pseudo-terminal pair through the Linux devpts
// multiplexer. The master's descriptor is returned for size ioctls.
func openPTY() (master *os.File, fd int, slave *os.File, err error) {
	fd, err = unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, -1, nil, err
	}
	master = os.NewFile(uintptr(fd), "worker-pty")
	if err = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		master.Close()
		return nil, -1, nil, err
	}
	number, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		master.Close()
		return nil, -1, nil, err
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, -1, nil, err
	}
	return master, fd, slave, nil
}
