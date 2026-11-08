//go:build linux

package worker

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
)

// TestManagerTimeoutReapsProcessGroup reproduces the orphaned-supervisor bug:
// a bounded provider probe whose child ignores its arguments and loops forever
// must leave neither the __marshal_process_group wrapper nor its child process
// group alive once the bound expires.
func TestManagerTimeoutReapsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pids")
	script := filepath.Join(dir, "codex")

	// The fake provider ignores its arguments, records its pid/pgid/parent and
	// a spawned child, then loops forever. It never writes stdout, so nothing
	// masks the hang.
	body := `#!/bin/sh
pf="$3"
echo "pid=$$ pgid=$(ps -o pgid= -p $$ | tr -d ' ') ppid=$PPID" > "$pf"
( while :; do sleep 1; done ) &
echo "child=$!" >> "$pf"
while :; do sleep 1; done
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	manager := New(250*time.Millisecond, 100*time.Millisecond, 1<<20)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	var result adapter.ProcessResult
	var runErr error
	go func() {
		defer close(done)
		result, runErr = manager.Run(ctx, adapter.Command{Path: script, Args: []string{"debug", "models", pidFile}})
	}()

	// The wrapper re-execs the marshal binary and the child must run first;
	// wait for the pid file rather than assuming the timeout already elapsed.
	waitFor(t, pidFile, time.Second)

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	supervisor, child, pgid := parsePids(t, data)

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("manager.Run did not return after the bound expired")
	}
	if !result.TimedOut {
		t.Fatalf("expected TimedOut, got %+v (err=%v)", result, runErr)
	}

	assertGone(t, "supervisor", supervisor, false)
	assertGone(t, "child", child, false)
	assertGone(t, "process-group", pgid, true)
}

func parsePids(t *testing.T, data []byte) (supervisor, child, pgid int) {
	t.Helper()
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "pid=") {
			continue
		}
		for _, field := range strings.Fields(line) {
			switch {
			case strings.HasPrefix(field, "pid="):
				child, _ = strconv.Atoi(strings.TrimPrefix(field, "pid="))
			case strings.HasPrefix(field, "pgid="):
				pgid, _ = strconv.Atoi(strings.TrimPrefix(field, "pgid="))
			case strings.HasPrefix(field, "ppid="):
				supervisor, _ = strconv.Atoi(strings.TrimPrefix(field, "ppid="))
			}
		}
	}
	if supervisor <= 0 || child <= 0 || pgid <= 0 {
		t.Fatalf("could not parse supervisor/child/pgid from %q", data)
	}
	return supervisor, child, pgid
}

func waitFor(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertGone(t *testing.T, label string, target int, group bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var err error
		if group {
			err = syscall.Kill(-target, 0)
		} else {
			err = syscall.Kill(target, 0)
		}
		if err == syscall.ESRCH {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("%s (target=%d group=%v) still alive: %v", label, target, group, err)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
