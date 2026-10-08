//go:build linux

package workerterminal

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/processgroup"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestProviderPTYUsesHostSizeAndResizes(t *testing.T) {
	var dimensions atomic.Uint32
	dimensions.Store(23<<16 | 80)
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := os.NewFile(uintptr(fd), "test-pty")
	defer master.Close()
	stop, err := propagateTerminalSize(t.Context(), fd, func(context.Context) (uint16, uint16, error) {
		size := dimensions.Load()
		return uint16(size >> 16), uint16(size), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	assertSize := func(rows, cols uint16) bool {
		size, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
		return err == nil && size.Row == rows && size.Col == cols
	}
	if !assertSize(23, 80) {
		t.Fatal("provider did not start at hosted pane size")
	}
	dimensions.Store(31<<16 | 101)
	deadline := time.Now().Add(time.Second)
	for !assertSize(31, 101) {
		if time.Now().After(deadline) {
			t.Fatal("resize did not reach provider PTY")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestInteractiveProviderForegroundInputAndSIGWINCH(t *testing.T) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := os.NewFile(uintptr(fd), "provider-test-pty")
	defer master.Close()
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer slave.Close()
	dir := t.TempDir()
	script := `stty size > "$1/initial"
trap 'stty size > "$1/resized"' WINCH
IFS= read -r input
printf '%s' "$input" > "$1/input"
while true; do read -r -t .1 ignored; done`
	cmd := exec.Command("/bin/bash", "-c", script, "provider", dir)
	if err := processgroup.Wrap(cmd); err != nil {
		t.Fatal(err)
	}
	configureInteractivePTY(cmd, slave)
	if err := unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Row: 23, Col: 80}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = processgroup.Stop(cmd); _ = cmd.Wait() }()
	waitFile := func(name, want string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err == nil && strings.TrimSpace(string(data)) == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s=%q err=%v want=%s", name, data, err, want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitFile("initial", "23 80")
	if _, err := master.Write([]byte("operator input\n")); err != nil {
		t.Fatal(err)
	}
	waitFile("input", "operator input")
	if err := unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Row: 31, Col: 101}); err != nil {
		t.Fatal(err)
	}
	waitFile("resized", "31 101")
}

func TestProviderPTYFallsBackAndRecoversHostSize(t *testing.T) {
	for _, initialError := range []bool{false, true} {
		t.Run(fmt.Sprint(initialError), func(t *testing.T) {
			fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer unix.Close(fd)
			var dimensions atomic.Uint32
			stop, err := propagateTerminalSize(t.Context(), fd, func(context.Context) (uint16, uint16, error) {
				d := dimensions.Load()
				if d == 0 && initialError {
					return 0, 0, io.EOF
				}
				return uint16(d >> 16), uint16(d), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer stop()
			assertSize := func(rows, cols uint16) bool {
				size, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
				return err == nil && size.Row == rows && size.Col == cols
			}
			if !assertSize(40, 120) {
				t.Fatal("missing sensible initial fallback")
			}
			dimensions.Store(23<<16 | 80)
			deadline := time.Now().Add(time.Second)
			for !assertSize(23, 80) {
				if time.Now().After(deadline) {
					t.Fatal("readable host size did not replace fallback")
				}
				time.Sleep(10 * time.Millisecond)
			}
			dimensions.Store(0)
			time.Sleep(250 * time.Millisecond)
			if !assertSize(23, 80) {
				t.Fatal("transient size failure replaced last real size")
			}
		})
	}
}
