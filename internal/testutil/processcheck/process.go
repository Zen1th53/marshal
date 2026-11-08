// Package processcheck checks worker cleanup against real background processes.
package processcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func Completion(t *testing.T, run func(string) error) {
	CompletionWithin(t, 2*time.Second, run)
}

// CompletionWithin allows host integrations to budget their own setup and
// teardown while retaining the same descendant-reaping assertions.
func CompletionWithin(t *testing.T, limit time.Duration, run func(string) error) {
	t.Helper()
	// Race-instrumented re-exec helpers otherwise sleep one second at exit,
	// which is unrelated to worker cleanup and exceeds this test's deadline.
	t.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	for _, kind := range []string{"closed-output", "inherited-output", "new-session", "double-fork"} {
		t.Run(kind, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "pid")
			child := "sleep 30"
			if kind == "new-session" {
				child = "setsid sleep 30"
			}
			if kind != "inherited-output" {
				child += " >/dev/null 2>&1"
			}
			script := fmt.Sprintf("%s &\necho $! > '%s'\nexit 0\n", child, pidFile)
			if kind == "double-fork" {
				script = fmt.Sprintf("setsid /bin/sh -c 'sleep 30 >/dev/null 2>&1 & echo $! > \"%s\"' >/dev/null 2>&1 &\nwhile [ ! -s '%s' ]; do sleep 0.01; done\nexit 0\n", pidFile, pidFile)
			}
			defer func() {
				data, _ := os.ReadFile(pidFile)
				pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
				if pid > 0 {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}()
			start := time.Now()
			err := run(script)
			if err != nil {
				t.Errorf("normal completion: %v", err)
			}
			if time.Since(start) > limit {
				t.Error("completion blocked by descendant output")
			}
			data, err := os.ReadFile(pidFile)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			dead := false
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
					dead = true
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !dead {
				t.Fatalf("descendant %d survived completion: %v", pid, syscall.Kill(pid, 0))
			}
		})
	}
}
