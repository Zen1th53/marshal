//go:build !governed

package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
)

func stallTestWorktree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return dir
}

func TestExtremeWorkerStallKillsSilentProcess(t *testing.T) {
	dir := stallTestWorktree(t)
	manager := New(5*time.Second, 100*time.Millisecond, 1<<20)
	start := time.Now()
	result, err := manager.Run(context.Background(), adapter.Command{
		Path: "/bin/sh", Args: []string{"-c", "sleep 30"}, Dir: dir,
		StallWindow: 120 * time.Millisecond, StallScanInterval: 25 * time.Millisecond, StallWorktree: dir,
	})
	if err == nil || !strings.Contains(err.Error(), "worker stalled: no output or worktree change for 120ms") || !result.Stalled || result.ExitCode == 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("stall termination was slow: %v", time.Since(start))
	}
}

func TestExtremeWorkerOutputKeepsWatchdogAlive(t *testing.T) {
	dir := stallTestWorktree(t)
	manager := New(5*time.Second, 100*time.Millisecond, 1<<20)
	result, err := manager.Run(context.Background(), adapter.Command{
		Path: "/bin/sh", Args: []string{"-c", "for i in 1 2 3 4 5; do printf x; sleep 0.06; done"}, Dir: dir,
		StallWindow: 120 * time.Millisecond, StallScanInterval: 20 * time.Millisecond, StallWorktree: dir,
	})
	if err != nil || result.Stalled || result.ExitCode != 0 || string(result.Stdout) != "xxxxx" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestExtremeWorkerWorktreeChangeKeepsWatchdogAlive(t *testing.T) {
	dir := stallTestWorktree(t)
	manager := New(5*time.Second, 100*time.Millisecond, 1<<20)
	result, err := manager.Run(context.Background(), adapter.Command{
		Path: "/bin/sh", Args: []string{"-c", "for i in 1 2 3 4 5; do printf x >> progress; sleep 0.06; done"}, Dir: dir,
		StallWindow: 120 * time.Millisecond, StallScanInterval: 20 * time.Millisecond, StallWorktree: dir,
	})
	data, readErr := os.ReadFile(filepath.Join(dir, "progress"))
	if err != nil || readErr != nil || result.Stalled || result.ExitCode != 0 || string(data) != "xxxxx" {
		t.Fatalf("result=%+v err=%v data=%q read=%v", result, err, data, readErr)
	}
}

func TestExtremeWorkerStallKeepsPartialWorktree(t *testing.T) {
	dir := stallTestWorktree(t)
	manager := New(5*time.Second, 100*time.Millisecond, 1<<20)
	result, err := manager.Run(context.Background(), adapter.Command{
		Path: "/bin/sh", Args: []string{"-c", "printf partial > progress; sleep 30"}, Dir: dir,
		StallWindow: 120 * time.Millisecond, StallScanInterval: 25 * time.Millisecond, StallWorktree: dir,
	})
	data, readErr := os.ReadFile(filepath.Join(dir, "progress"))
	if err == nil || !result.Stalled || readErr != nil || string(data) != "partial" {
		t.Fatalf("result=%+v err=%v data=%q read=%v", result, err, data, readErr)
	}
}

func TestWorkerStallWatchIgnoresExternalSymlinkTargetAndUsesHostGit(t *testing.T) {
	dir := stallTestWorktree(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	hook := filepath.Join(t.TempDir(), "monitor")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "config", "core.fsmonitor", hook).CombinedOutput(); err != nil {
		t.Fatalf("config: %v %s", err, out)
	}
	w := newWorkerStallWatch(adapter.Command{StallWorktree: dir, StallWindow: time.Second})
	if _, err := w.scan(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("a changed, longer target"), 0600); err != nil {
		t.Fatal(err)
	}
	if !w.stalled(w.last.Add(2 * time.Second)) {
		t.Fatal("external target activity reset stall timer")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("fsmonitor executed: %v", err)
	}
}
