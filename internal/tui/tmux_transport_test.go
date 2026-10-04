//go:build linux

package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/tmux"
)

func realTmuxWorkspace(t *testing.T) *Workspace {
	t.Helper()
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "tmux.sock")
	wrapper := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec "+bin+" -S "+sock+" \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(wrapper)
	t.Cleanup(func() { exec.Command(bin, "-S", sock, "kill-server").Run(); tmux.ResetBinaryPath() })
	ctx := context.Background()
	if _, err := tmux.RunCommand(ctx, "new-session", "-d", "-s", "test", "-n", "marshal", "sleep", "600"); err != nil {
		t.Fatal(err)
	}
	w := NewWorkspace(nil, "project", "session")
	w.workDir = t.TempDir()
	w.tmuxPath = wrapper
	w.tmuxSession = "test"
	w.tmuxMarshalWin = "test:marshal"
	t.Setenv("TMUX", sock+",1,0")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "base"}} {
		c := exec.Command("git", args...)
		c.Dir = w.workDir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%s %v", out, err)
		}
	}
	return w
}

func realTaskRequest(t *testing.T, w *Workspace, id string) driver.Request {
	c := exec.Command("git", "rev-parse", "HEAD")
	c.Dir = w.workDir
	out, err := c.Output()
	if err != nil {
		t.Fatal(err)
	}
	return driver.Request{Task: marshal.Task{PlanTaskID: id, Worker: "test", BaseCommit: strings.TrimSpace(string(out))}, Worktree: w.workDir, Brief: "task instruction"}
}

func TestTaskTerminalShowsDriverOutputAndAcceptsTakeover(t *testing.T) {
	w := realTmuxWorkspace(t)
	d := &tmuxTaskDriver{w: w, inner: driver.Native{Provider: "test", Binary: "/bin/sh", Args: func(driver.Request) []string {
		return []string{"-c", "echo DRIVER_OUTPUT; read answer; echo received:$answer"}
	}, Parse: func([]byte) []marshal.CommandRecord { return nil }}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h, err := d.Launch(ctx, realTaskRequest(t, w, "terminal"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Cancel(h)
	w.tmuxMu.Lock()
	a := w.tmuxActiveWins["task-terminal"]
	w.tmuxMu.Unlock()
	if a == nil {
		t.Fatal("no task terminal")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		out, _ := tmux.CapturePane(ctx, a.paneID)
		if strings.Contains(out, "DRIVER_OUTPUT") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("driver output missing: %q", out)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := w.handleTakeoverCommand(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := tmux.RunCommand(ctx, "send-keys", "-t", a.paneID, "operator-input", "Enter"); err != nil {
		t.Fatal(err)
	}
	result, err := d.Wait(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RuntimeObserved) == 0 || !strings.Contains(result.RuntimeObserved[0].Output, "received:operator-input") {
		t.Fatalf("worker did not receive terminal input: %+v", result.RuntimeObserved)
	}
}
