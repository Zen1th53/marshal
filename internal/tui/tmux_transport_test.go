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
	cleanupTmuxWorkspace(t, w)
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
	if err := os.WriteFile(filepath.Join(w.workDir, ".git", "info", "exclude"), []byte(".marshal/\n"), 0600); err != nil {
		t.Fatal(err)
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

// Launch returns before a worker necessarily produces output. Evidence tests
// must observe the fixture's output before asking StopAll to cancel it.
func waitForTmuxOutput(t *testing.T, pane, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		out, err := tmux.CapturePane(ctx, pane)
		if err != nil {
			t.Fatalf("capture %s waiting for %q: %v", pane, want, err)
		}
		if strings.Contains(out, want) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pane %s missing %q: %q", pane, want, out)
		case <-ticker.C:
		}
	}
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
	assertNativePaneInput(t, ctx, a.paneID, "1")
	if _, err := w.handleTakeoverCommand(ctx); err != nil {
		t.Fatal(err)
	}
	assertNativePaneInput(t, ctx, a.paneID, "0")
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

func TestStopAllUsesTaskIdentityAfterRenameAndJoin(t *testing.T) {
	w := realTmuxWorkspace(t)
	ctx := context.Background()
	d := &tmuxTaskDriver{w: w, inner: driver.Native{Provider: "test", Binary: "/bin/sh", Args: func(driver.Request) []string { return []string{"-c", "echo TASK_EVIDENCE; sleep 30"} }, Parse: func([]byte) []marshal.CommandRecord { return nil }}}
	h, err := d.Launch(ctx, realTaskRequest(t, w, "immutable"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Cancel(h)
	w.tmuxMu.Lock()
	a := w.tmuxActiveWins["task-immutable"]
	w.tmuxMu.Unlock()
	waitForTmuxOutput(t, a.paneID, "TASK_EVIDENCE")
	if _, err := tmux.RunCommand(ctx, "rename-window", "-t", a.paneID, "user-renamed"); err != nil {
		t.Fatal(err)
	}
	if err := tmux.JoinPane(ctx, a.paneID, w.tmuxMarshalWin, true); err != nil {
		t.Fatal(err)
	}
	// A fresh workspace must recover task identity without inspecting names/titles.
	recovered := NewWorkspace(nil, "project", "session")
	cleanupTmuxWorkspace(t, recovered)
	recovered.workDir = w.workDir
	recovered.tmuxPath = w.tmuxPath
	recovered.tmuxSession = w.tmuxSession
	recovered.tmuxMarshalWin = w.tmuxMarshalWin
	panes, _ := tmux.ListPanes(ctx, w.tmuxSession)
	for _, p := range panes {
		if p.WindowName == "marshal" && p.PaneID != a.paneID {
			recovered.tmuxMarshalPaneID = p.PaneID
		}
	}
	recovered.tmuxMu.Lock()
	recovered.adoptSurvivingWorkers(w.workDir)
	adopted := recovered.tmuxActiveWins["task-immutable"]
	recovered.tmuxMu.Unlock()
	if adopted == nil || adopted.taskID != "immutable" {
		t.Fatal("renamed/joined task was not recovered by immutable metadata")
	}
	response := recovered.StopAllWorkers(ctx)
	if !strings.Contains(response, "Stopped all") {
		t.Fatal(response)
	}
	panes, err = tmux.ListPanes(ctx, w.tmuxSession)
	if err != nil {
		t.Fatal("stop-all destroyed Marshal session:", err)
	}
	for _, p := range panes {
		if p.PaneID == a.paneID {
			t.Fatal("task terminal survives stop-all")
		}
	}
	evidence, err := os.ReadFile(filepath.Join(w.workDir, ".marshal", "evidence", "task-immutable-latest.txt"))
	if err != nil || !strings.Contains(string(evidence), "TASK_EVIDENCE") {
		t.Fatalf("task evidence lost: %q %v", evidence, err)
	}
}

func TestEvidenceFailureRetainsStoppedTaskPane(t *testing.T) {
	w := realTmuxWorkspace(t)
	d := &tmuxTaskDriver{w: w, inner: driver.Native{Provider: "test", Binary: "/bin/sh", Args: func(driver.Request) []string { return []string{"-c", "echo task; read answer"} }, Parse: func([]byte) []marshal.CommandRecord { return nil }}}
	h, err := d.Launch(context.Background(), realTaskRequest(t, w, "evidence-failure"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Cancel(h)
	path := filepath.Join(w.workDir, ".marshal", "evidence")
	os.MkdirAll(filepath.Dir(path), 0700)
	if err := os.WriteFile(path, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	response := w.StopAllWorkers(context.Background())
	if !strings.Contains(response, "evidence") {
		t.Fatalf("capture error was discarded: %s", response)
	}
	w.tmuxMu.Lock()
	a := w.tmuxActiveWins["task-evidence-failure"]
	w.tmuxMu.Unlock()
	if a == nil {
		t.Fatal("pane discarded after evidence failure")
	}
	panes, _ := tmux.ListPanes(context.Background(), w.tmuxSession)
	for _, p := range panes {
		if p.PaneID == a.paneID {
			return
		}
	}
	t.Fatal("evidence failure destroyed terminal")
}

func TestConcurrentRunsKeepSameTaskIDsSeparate(t *testing.T) {
	w := realTmuxWorkspace(t)
	d := &tmuxTaskDriver{w: w, inner: driver.Native{Provider: "test", Binary: "/bin/sh", Args: func(req driver.Request) []string {
		return []string{"-c", "echo evidence:$1; read answer", "fixture", req.RunID}
	}, Parse: func([]byte) []marshal.CommandRecord { return nil }}}
	for _, run := range []string{"first", "second"} {
		req := realTaskRequest(t, w, "same")
		req.RunID = run
		h, err := d.Launch(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		defer d.Cancel(h)
	}
	w.tmuxMu.Lock()
	var workers int
	panes := make(map[string]string)
	for _, a := range w.tmuxActiveWins {
		if a.role == "task" {
			workers++
			panes[a.runID] = a.paneID
		}
	}
	w.tmuxMu.Unlock()
	if workers != 2 {
		t.Fatalf("concurrent task identities collided: %d workers", workers)
	}
	for _, run := range []string{"first", "second"} {
		waitForTmuxOutput(t, panes[run], "evidence:"+run)
	}
	if response := w.StopAllWorkers(context.Background()); !strings.Contains(response, "Stopped all") {
		t.Fatal(response)
	}
	for _, run := range []string{"first", "second"} {
		data, err := os.ReadFile(filepath.Join(w.workDir, ".marshal", "evidence", "task-"+run+"-same-latest.txt"))
		if err != nil || !strings.Contains(string(data), "evidence:"+run) {
			t.Fatalf("run %s evidence is not bound to task identity: %q %v", run, data, err)
		}
	}
}
