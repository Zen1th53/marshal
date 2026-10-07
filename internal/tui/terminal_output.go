package tui

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"time"

	"golang.org/x/sys/unix"
)

// terminalOutput uses a separately opened nonblocking descriptor: changing the
// flags of stdout itself would also change a native child's inherited terminal.
// A stalled tmux server must not leave a paint waiting forever for PTY capacity.
type terminalOutput struct {
	fd      int
	timeout time.Duration
}

func (t *Terminal) boundOutput() (func(), error) {
	if t.outFd < 0 || runtime.GOOS != "linux" {
		return func() {}, nil
	}
	fd, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", t.outFd), unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, fmt.Errorf("open bounded terminal output: %w", err)
	}
	original := t.out
	t.out = &terminalOutput{fd: fd, timeout: 250 * time.Millisecond}
	return func() { t.out = original; _ = unix.Close(fd) }, nil
}

func (w *terminalOutput) Write(p []byte) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), w.timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	written := 0
	for len(p) > 0 {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		n, err := unix.Write(w.fd, p)
		if n > 0 {
			written += n
			p = p[n:]
		}
		if err == unix.EINTR {
			continue
		}
		if err == unix.EAGAIN {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return written, context.DeadlineExceeded
			}
			_, err = unix.Poll([]unix.PollFd{{Fd: int32(w.fd), Events: unix.POLLOUT}}, int((remaining+time.Millisecond-1)/time.Millisecond))
			if err == unix.EINTR {
				continue
			}
			if err != nil {
				return written, err
			}
			continue
		}
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrNoProgress
		}
	}
	return written, nil
}
