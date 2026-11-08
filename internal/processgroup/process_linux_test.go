//go:build linux

package processgroup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func waitProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		// An orphan can briefly remain as a zombie until init reaps it.
		if data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat")); err == nil {
			if fields := strings.Fields(string(data)); len(fields) > 2 && fields[2] == "Z" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("worker process %d survived", pid)
}

// startSupervised writes a fake provider script and starts it under a
// supervisor, returning the command and the provider's pid once it is running.
// Extra marker paths are appended to the provider's argv.
func startSupervised(t *testing.T, body string, extra ...string) (*exec.Cmd, int) {
	t.Helper()
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	script := filepath.Join(dir, "provider")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	args := append([]string{pidFile}, extra...)
	cmd := exec.Command(script, args...)
	if err := Wrap(cmd); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = Kill(cmd)
		_ = cmd.Wait()
	})
	deadline := time.Now().Add(time.Second)
	for {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			text := strings.TrimSpace(string(data))
			if text != "" {
				pid, err := strconv.Atoi(text)
				if err != nil || pid <= 0 {
					t.Fatalf("bad pid file %q: %v", data, err)
				}
				if strings.HasSuffix(string(data), "\n") {
					time.Sleep(5 * time.Millisecond)
					return cmd, pid
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("provider pid file never appeared")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitSupervised waits for the supervisor to exit after Stop/Kill.
func waitSupervised(t *testing.T, cmd *exec.Cmd) error {
	t.Helper()
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err := <-wait:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor did not exit")
		return nil
	}
}

// TestStopForwardsSIGTERMGracefully confirms the wrapper hands SIGTERM to the
// child group first: a provider that traps SIGTERM and exits 0 is reaped with
// its own exit status, not force-killed.
func TestStopForwardsSIGTERMGracefully(t *testing.T) {
	termFile := filepath.Join(t.TempDir(), "term")
	cmd, _ := startSupervised(t, `#!/bin/sh
trap 'echo term > "$2"; exit 0' TERM
echo "$$" > "$1"
while :; do sleep 1; done
`, termFile)

	if err := Stop(cmd); err != nil {
		t.Fatal(err)
	}
	if err := waitSupervised(t, cmd); err != nil {
		t.Fatalf("provider did not exit cleanly on SIGTERM: %v", err)
	}
	if _, err := os.Stat(termFile); err != nil {
		t.Fatalf("provider never received SIGTERM: %v", err)
	}
}

// TestStopEscalatesToSIGKILL confirms the wrapper force-kills a provider that
// ignores SIGTERM, so the child group cannot keep the supervisor alive.
func TestStopEscalatesToSIGKILL(t *testing.T) {
	cmd, pid := startSupervised(t, `#!/bin/sh
trap '' TERM
echo "$$" > "$1"
while :; do sleep 1; done
`)
	if err := Stop(cmd); err != nil {
		t.Fatal(err)
	}
	if err := waitSupervised(t, cmd); err == nil {
		t.Fatal("expected non-zero exit from force-killed provider")
	}
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatalf("provider %d survived Stop: %v", pid, err)
	}
}

// TestKillTerminatesSupervisor confirms the last-resort Kill reaps the wrapper
// even when the child group is still alive, so the caller's Wait returns.
func TestKillTerminatesSupervisor(t *testing.T) {
	cmd, pid := startSupervised(t, `#!/bin/sh
echo "$$" > "$1"
while :; do sleep 1; done
`)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if err := Kill(cmd); err != nil {
		t.Fatal(err)
	}
	if err := waitSupervised(t, cmd); err == nil {
		t.Fatal("expected non-zero exit from killed supervisor")
	}
}

func TestContextCancellationReapsWorkerGroup(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "worker")
	pidFile := filepath.Join(dir, "child-pid")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30 &\necho $! > \"$1\"\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cmd := exec.CommandContext(ctx, script, pidFile)
	if err := Wrap(cmd); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = Kill(cmd); _ = cmd.Wait() })
	pid := readPIDFile(t, pidFile)
	cancel()
	if err := waitSupervised(t, cmd); err == nil {
		t.Fatal("cancelled worker exited successfully")
	}
	waitProcessGone(t, pid)
}

func TestParentDeathKillsWorker(t *testing.T) {
	if os.Getenv("MARSHAL_PROCESSGROUP_PARENT_HELPER") == "1" {
		script := os.Getenv("MARSHAL_PROCESSGROUP_CHILD_SCRIPT")
		pidFile := os.Getenv("MARSHAL_PROCESSGROUP_CHILD_PID")
		cmd := exec.Command(script, pidFile)
		if err := Wrap(cmd); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		readPIDFile(t, pidFile)
		for {
			time.Sleep(time.Second)
		}
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "worker")
	pidFile := filepath.Join(dir, "pid")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho $$ > \"$1\"\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(os.Args[0], "-test.run=^TestParentDeathKillsWorker$")
	parent.Env = append(os.Environ(), "MARSHAL_PROCESSGROUP_PARENT_HELPER=1", "MARSHAL_PROCESSGROUP_CHILD_SCRIPT="+script, "MARSHAL_PROCESSGROUP_CHILD_PID="+pidFile)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Process.Kill(); _ = parent.Wait() })
	pid := readPIDFile(t, pidFile)
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	waitProcessGone(t, pid)
}

func readPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && strings.HasSuffix(string(data), "\n") {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("pid file %s never appeared", path)
	return 0
}
