package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/tmux"
)

func setupFakeTmuxWithDeadFile(t *testing.T) (fakeTmuxPath, logFile, deadFile string) {
	t.Helper()
	tempDir := t.TempDir()
	logFile = filepath.Join(tempDir, "tmux_argv.log")
	winFile := filepath.Join(tempDir, "tmux_windows.log")
	deadFile = filepath.Join(tempDir, "tmux_dead.flag")
	fakeTmuxPath = filepath.Join(tempDir, "tmux")

	script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %q
winFile=%q
deadFile=%q
case "$1" in
  new-window)
    prev=""
    for a in "$@"; do
      if [ "$prev" = "-n" ]; then
        echo "$a" >> "$winFile"
      fi
      prev="$a"
    done
    exit 0
    ;;
  respawn-window)
    exit 0
    ;;
  display-message)
    case "$*" in
      *"#{session_id}"*)
        printf '$0\n'
        exit 0
        ;;
      *"#{session_name}:#{window_id}"*)
        printf 'test-session:@0\n'
        exit 0
        ;;
      *"#{session_name}"*"#{window_name}"*"#{window_id}"*)
        printf 'test-session\tmarshal\t@0\n'
        exit 0
        ;;
      *"#{session_name}"*)
        printf 'test-session\n'
        exit 0
        ;;
      *"#{pane_id}"*)
        # A targeted query must resolve that pane, not default to Marshal.
        target=""
        prev=""
        for a in "$@"; do
          if [ "$prev" = "-t" ]; then target="$a"; fi
          prev="$a"
        done
        case "$target" in
          "") printf '%%%%0\n'; exit 0 ;;
          %%) exit 1 ;;
          %%*[!0-9]*) exit 1 ;;
          %%*) printf '%%s\n' "$target"; exit 0 ;;
          marshal|*:marshal) printf '%%%%0\n'; exit 0 ;;
        esac
        if [ -f "$winFile" ]; then
          idx=1
          while read -r w; do
            if [ "$target" = "$w" ] || [ "${target#*:}" = "$w" ]; then
              printf '%%%%%%s\n' "$idx"
              exit 0
            fi
            idx=$((idx + 1))
          done < "$winFile"
        fi
        exit 1
        ;;
      *"#{pane_pid}"*)
        printf '100\n'
        exit 0
        ;;
      *"#{pane_dead}"*)
        if [ -f "$deadFile" ]; then
          printf '1 0\n'
        else
          printf '0 0\n'
        fi
        exit 0
        ;;
    esac
    exit 0
    ;;
  list-panes)
    printf '%%%%0\t@0\tmarshal\t100\t0\t0\t\n'
    if [ -f "$winFile" ]; then
      idx=1
      while read -r w; do
        printf '%%%%%%s\t@%%s\t%%s\t%%s\t0\t0\t\n' "$idx" "$idx" "$w" "$((100 + idx))"
        idx=$((idx + 1))
      done < "$winFile"
    fi
    exit 0
    ;;
  list-windows)
    printf 'marshal\n'
    if [ -f "$winFile" ]; then
      cat "$winFile"
    fi
    exit 0
    ;;
  capture-pane)
    printf 'Recorded agent output evidence line 1\nRecorded agent output evidence line 2\n'
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
`, logFile, winFile, deadFile)

	if err := os.WriteFile(fakeTmuxPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	tmux.SetBinaryPath(fakeTmuxPath)
	t.Cleanup(func() {
		tmux.ResetBinaryPath()
	})
	return fakeTmuxPath, logFile, deadFile
}

func setupFakeTmux(t *testing.T) (fakeTmuxPath, logFile string) {
	fakeTmuxPath, logFile, _ = setupFakeTmuxWithDeadFile(t)
	return fakeTmuxPath, logFile
}

func TestTmuxNativeAgentOpenAndSwitch(t *testing.T) {
	_, logFile := setupFakeTmux(t)
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")

	workDir := t.TempDir()
	ws := NewWorkspace(nil, "test-proj", "test-session")
	ws.workDir = workDir
	cleanupTmuxWorkspace(t, ws)
	ws.InitTmux(workDir)

	ctx := context.Background()

	// 1. Open Codex in tmux
	msg, err := ws.runNativeAgentInTmux(ctx, "codex", "Codex", workDir, "echo", []string{"hello"}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("runNativeAgentInTmux failed: %v", err)
	}
	if !strings.Contains(msg, "Opened native Codex in tmux window") {
		t.Fatalf("unexpected message: %s", msg)
	}
	if !strings.Contains(msg, "F11 to return to MARSHAL") {
		t.Fatalf("message should mention F11 return: %s", msg)
	}

	codexWin := tmux.WindowName("codex", workDir)

	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logBytes)

	// Verify new-window, select-pane -d (view-only), select-window, bind-key F7 and F11
	if !strings.Contains(logStr, "new-window -t test-session -n "+codexWin) {
		t.Fatalf("missing new-window call:\n%s", logStr)
	}
	if !strings.Contains(logStr, "select-pane -t test-session:"+codexWin+" -d") {
		t.Fatalf("missing view-only select-pane -d call:\n%s", logStr)
	}
	if !strings.Contains(logStr, "select-window -t test-session:"+codexWin) {
		t.Fatalf("missing select-window call:\n%s", logStr)
	}
	if !strings.Contains(logStr, "bind-key -T marshal-keys-"+tmux.ProjectHash(workDir)+" F7 if-shell -F") {
		t.Fatalf("missing F7 bind-key call:\n%s", logStr)
	}
	if !strings.Contains(logStr, "bind-key -T marshal-keys-"+tmux.ProjectHash(workDir)+" F11 if-shell -F") {
		t.Fatalf("missing F11 bind-key call:\n%s", logStr)
	}

	// 2. Open Codex again (switching)
	switchMsg, err := ws.runNativeAgentInTmux(ctx, "codex", "Codex", workDir, "echo", []string{"hello"}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("second runNativeAgentInTmux failed: %v", err)
	}
	if !strings.Contains(switchMsg, "Switched to active Codex session") {
		t.Fatalf("expected switch message, got: %s", switchMsg)
	}
	if !strings.Contains(switchMsg, "continues running") {
		t.Fatalf("expected mention that session continues running: %s", switchMsg)
	}
}

func TestTmuxViewCommands(t *testing.T) {
	_, logFile := setupFakeTmux(t)
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")

	workDir := t.TempDir()
	ws := NewWorkspace(nil, "test-proj", "test-session")
	ws.workDir = workDir
	cleanupTmuxWorkspace(t, ws)
	ws.InitTmux(workDir)
	ctx := context.Background()

	// Launch a dummy worker
	_, err := ws.runNativeAgentInTmux(ctx, "claude", "Claude", workDir, "echo", []string{"hello"}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	claudeWin := tmux.WindowName("claude", workDir)

	// Test /view focus
	resp, err := ws.handleViewCommand(ctx, []string{"focus"})
	if err != nil || !strings.Contains(resp, "focus") {
		t.Fatalf("/view focus failed: %s, err=%v", resp, err)
	}

	// Test /view side-by-side
	resp, err = ws.handleViewCommand(ctx, []string{"side-by-side"})
	if err != nil || !strings.Contains(resp, "side-by-side") {
		t.Fatalf("/view side-by-side failed: %s, err=%v", resp, err)
	}

	// Test /view worker
	resp, err = ws.handleViewCommand(ctx, []string{"worker"})
	if err != nil || !strings.Contains(resp, "active worker") {
		t.Fatalf("/view worker failed: %s, err=%v", resp, err)
	}

	// Test /view show claude
	resp, err = ws.handleViewCommand(ctx, []string{"show", "claude"})
	if err != nil || !strings.Contains(resp, "showing Claude") {
		t.Fatalf("/view show claude failed: %s, err=%v", resp, err)
	}

	// Test /view hide
	resp, err = ws.handleViewCommand(ctx, []string{"hide"})
	if err != nil || !strings.Contains(resp, "hidden other panes") {
		t.Fatalf("/view hide failed: %s, err=%v", resp, err)
	}

	// Test /view follow
	resp, err = ws.handleViewCommand(ctx, []string{"follow"})
	if err != nil || !strings.Contains(resp, "follow-active is now true") {
		t.Fatalf("/view follow failed: %s, err=%v", resp, err)
	}

	// Test /view readonly
	resp, err = ws.handleViewCommand(ctx, []string{"readonly"})
	if err != nil || !strings.Contains(resp, "view-only") {
		t.Fatalf("/view readonly failed: %s, err=%v", resp, err)
	}

	// Test /takeover
	resp, err = ws.handleTakeoverCommand(ctx)
	if err != nil || !strings.Contains(resp, "input enabled") {
		t.Fatalf("/takeover failed: %s, err=%v", resp, err)
	}

	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logBytes)
	if !strings.Contains(logStr, "select-window -t test-session:"+claudeWin) {
		t.Fatalf("missing select-window in log:\n%s", logStr)
	}
	if !strings.Contains(logStr, "join-pane -h -s ") {
		t.Fatalf("missing join-pane in log:\n%s", logStr)
	}
	if !strings.Contains(logStr, "break-pane -s ") {
		t.Fatalf("missing break-pane in log:\n%s", logStr)
	}
	if !strings.Contains(logStr, "-e") {
		t.Fatalf("missing select-pane -e in log:\n%s", logStr)
	}
}

func TestStopAllWorkersPreservesMarshal(t *testing.T) {
	_, logFile := setupFakeTmux(t)
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")

	workDir := t.TempDir()
	ws := NewWorkspace(nil, "test-proj", "test-session")
	ws.workDir = workDir
	cleanupTmuxWorkspace(t, ws)
	ws.InitTmux(workDir)
	ctx := context.Background()

	// Launch two workers
	if _, err := ws.runNativeAgentInTmux(ctx, "codex", "Codex", workDir, "echo", []string{"hello"}, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.runNativeAgentInTmux(ctx, "claude", "Claude", workDir, "echo", []string{"hello"}, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	ws.tmuxMu.Lock()
	codexPane := ws.tmuxActiveWins["codex"].paneID
	claudePane := ws.tmuxActiveWins["claude"].paneID
	ws.tmuxMu.Unlock()

	// Stop all workers
	resp := ws.StopAllWorkers(ctx)
	if !strings.Contains(resp, "Stopped all worker sessions") {
		t.Fatalf("unexpected StopAllWorkers response: %s", resp)
	}
	if !strings.Contains(resp, "MARSHAL remains active") {
		t.Fatalf("expected mention that MARSHAL remains active: %s", resp)
	}

	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logBytes)

	// Both worker windows must be killed
	if !strings.Contains(logStr, "kill-pane -t "+codexPane) {
		t.Fatalf("codex window was not killed:\n%s", logStr)
	}
	if !strings.Contains(logStr, "kill-pane -t "+claudePane) {
		t.Fatalf("claude window was not killed:\n%s", logStr)
	}
	// Marshal window MUST NOT be killed!
	for _, line := range strings.Split(logStr, "\n") {
		if strings.TrimSpace(line) == "kill-window -t marshal" {
			t.Fatalf("marshal window was killed:\n%s", logStr)
		}
	}

	// Verify evidence was saved for both workers
	evidenceDir := filepath.Join(workDir, ".marshal", "evidence")
	codexLatest := filepath.Join(evidenceDir, "codex-latest.txt")
	claudeLatest := filepath.Join(evidenceDir, "claude-latest.txt")

	if _, err := os.Stat(codexLatest); err != nil {
		t.Fatalf("codex evidence was not saved: %v", err)
	}
	if _, err := os.Stat(claudeLatest); err != nil {
		t.Fatalf("claude evidence was not saved: %v", err)
	}
}

func TestStopAllWorkersPreservesMarshalChat(t *testing.T) {
	_, logFile := setupFakeTmux(t)
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")

	workDir := t.TempDir()
	ws := NewWorkspace(nil, "test-proj", "test-session")
	ws.workDir = workDir
	cleanupTmuxWorkspace(t, ws)
	ws.InitTmux(workDir)
	ctx := context.Background()

	// 1. Launch a regular worker (isChat = false)
	_, _ = ws.runNativeAgentInTmux(ctx, "claude", "Claude", workDir, "echo", []string{"hello"}, nil, nil, nil, nil, nil, nil, nil, false)
	ws.tmuxMu.Lock()
	claudePane := ws.tmuxActiveWins["claude"].paneID
	ws.tmuxMu.Unlock()

	// 2. Launch Marshal planning chat (isChat = true)
	_, _ = ws.runNativeAgentInTmux(ctx, "claude", "Claude", workDir, "echo", []string{"planning"}, nil, nil, nil, nil, nil, nil, nil, true)
	chatWin := tmux.ChatWindowName(workDir)

	ws.tmuxMu.Lock()
	if _, ok := ws.tmuxActiveWins["claude"]; !ok {
		t.Fatal("claude worker missing from active wins")
	}
	if _, ok := ws.tmuxActiveWins["marshal-chat"]; !ok {
		t.Fatal("marshal-chat missing from active wins")
	}
	ws.tmuxMu.Unlock()

	// 3. Stop all workers
	resp := ws.StopAllWorkers(ctx)
	if !strings.Contains(resp, "Stopped all worker sessions") {
		t.Fatalf("unexpected StopAllWorkers response: %s", resp)
	}

	ws.tmuxMu.Lock()
	// Worker must be deleted
	if _, ok := ws.tmuxActiveWins["claude"]; ok {
		t.Fatal("claude worker was NOT removed from active wins")
	}
	// Marshal chat MUST SURVIVE!
	chatAgent, ok := ws.tmuxActiveWins["marshal-chat"]
	if !ok || chatAgent == nil {
		t.Fatal("marshal-chat was killed by StopAllWorkers!")
	}
	if chatAgent.role != "marshal-chat" {
		t.Fatalf("marshal-chat role altered: %s", chatAgent.role)
	}
	ws.tmuxMu.Unlock()

	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logBytes)

	// Worker window killed
	if !strings.Contains(logStr, "kill-pane -t "+claudePane) {
		t.Fatalf("claude window was not killed:\n%s", logStr)
	}
	// Marshal chat window MUST NOT be killed!
	if strings.Contains(logStr, "kill-window -t "+chatWin) {
		t.Fatalf("marshal-chat window was killed by StopAllWorkers:\n%s", logStr)
	}
}

func TestJoinPanePreservesWorkerIdentityAndMonitoring(t *testing.T) {
	_, logFile := setupFakeTmux(t)
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")

	workDir := t.TempDir()
	ws := NewWorkspace(nil, "test-proj", "test-session")
	ws.workDir = workDir
	cleanupTmuxWorkspace(t, ws)
	ws.InitTmux(workDir)
	ctx := context.Background()

	// Launch worker
	_, _ = ws.runNativeAgentInTmux(ctx, "claude", "Claude", workDir, "echo", []string{"hello"}, nil, nil, nil, nil, nil, nil, nil, false)
	claudeWin := tmux.WindowName("claude", workDir)

	ws.tmuxMu.Lock()
	agent, ok := ws.tmuxActiveWins["claude"]
	if !ok {
		t.Fatal("claude worker missing")
	}
	if agent.window != claudeWin {
		t.Fatalf("window mismatch: %s vs %s", agent.window, claudeWin)
	}
	origPaneID := agent.paneID
	ws.tmuxMu.Unlock()

	// Test side-by-side view (join-pane)
	resp, err := ws.handleViewCommand(ctx, []string{"side-by-side"})
	if err != nil || !strings.Contains(resp, "side-by-side") {
		t.Fatalf("/view side-by-side failed: %s, err=%v", resp, err)
	}

	ws.tmuxMu.Lock()
	if !agent.isJoined {
		t.Fatal("expected agent.isJoined to be true")
	}
	if agent.paneID != origPaneID {
		t.Fatalf("paneID changed after join: %s vs %s", agent.paneID, origPaneID)
	}
	ws.tmuxMu.Unlock()

	// Test hide (break-pane back)
	resp, err = ws.handleViewCommand(ctx, []string{"hide"})
	if err != nil || !strings.Contains(resp, "hidden") {
		t.Fatalf("/view hide failed: %s, err=%v", resp, err)
	}

	ws.tmuxMu.Lock()
	if agent.isJoined {
		t.Fatal("expected agent.isJoined to be false after hide")
	}
	ws.tmuxMu.Unlock()

	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logBytes)

	if !strings.Contains(logStr, "join-pane -h -s ") {
		t.Fatalf("missing join-pane in log:\n%s", logStr)
	}
	if !strings.Contains(logStr, "break-pane -s ") {
		t.Fatalf("missing break-pane in log:\n%s", logStr)
	}
}

func TestSurvivorAdoptionOnReattach(t *testing.T) {
	tempDir := t.TempDir()
	logFile := filepath.Join(tempDir, "tmux_argv.log")
	winFile := filepath.Join(tempDir, "tmux_windows.log")
	fakeTmuxPath := filepath.Join(tempDir, "tmux")

	workDir := t.TempDir()
	hash := tmux.ProjectHash(workDir)
	claudeWin := fmt.Sprintf("marshal-claude-%s", hash)
	chatWin := fmt.Sprintf("marshal-chat-%s", hash)

	// Write surviving windows into winFile
	_ = os.WriteFile(winFile, []byte(claudeWin+"\n"+chatWin+"\n"), 0o644)

	script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %q
winFile=%q
case "$1" in
  display-message)
    case "$*" in
      *"#{session_name}"*"#{window_name}"*"#{window_id}"*)
        printf 'test-session\tmarshal\t@0\n'
        exit 0
        ;;
      *"#{session_name}"*)
        printf 'test-session\n'
        exit 0
        ;;
      *"#{pane_id}"*)
        printf '%%%%0\n'
        exit 0
        ;;
      *"#{pane_dead}"*)
        printf '0\n'
        exit 0
        ;;
    esac
    exit 0
    ;;
  list-panes)
    printf '%%%%0\t@0\tmarshal\t100\t0\t0\n'
    printf '%%%%1\t@1\t%s\t101\t0\t0\n'
    printf '%%%%2\t@2\t%s\t102\t0\t0\n'
    exit 0
    ;;
  list-windows)
    printf 'marshal\n%s\n%s\n'
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
`, logFile, winFile, claudeWin, chatWin, claudeWin, chatWin)

	if err := os.WriteFile(fakeTmuxPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	tmux.SetBinaryPath(fakeTmuxPath)
	defer tmux.ResetBinaryPath()

	t.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")

	ws := NewWorkspace(nil, "test-proj", "test-session")
	ws.workDir = workDir
	if err := saveAgentRecord(workDir, "test-session", &activeTmuxAgent{id: "claude", role: "worker", provider: "claude", window: claudeWin, paneID: "%1"}); err != nil {
		t.Fatal(err)
	}
	if err := saveAgentRecord(workDir, "test-session", &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", window: chatWin, paneID: "%2"}); err != nil {
		t.Fatal(err)
	}
	cleanupTmuxWorkspace(t, ws)
	ws.InitTmux(workDir)

	ws.tmuxMu.Lock()
	defer ws.tmuxMu.Unlock()

	// Verify both survivors were adopted
	worker, hasWorker := ws.tmuxActiveWins["claude"]
	if !hasWorker || worker == nil {
		t.Fatal("surviving claude worker was not adopted")
	}
	if worker.role != "worker" || worker.window != claudeWin {
		t.Fatalf("worker improperly adopted: %+v", worker)
	}

	chat, hasChat := ws.tmuxActiveWins["marshal-chat"]
	if !hasChat || chat == nil {
		t.Fatal("surviving marshal-chat was not adopted")
	}
	if chat.role != "marshal-chat" || chat.window != chatWin {
		t.Fatalf("chat improperly adopted: %+v", chat)
	}
}

type testMockDriver struct{}

func (m *testMockDriver) Mode() marshal.WorkerMode {
	return marshal.Native
}

func (m *testMockDriver) Launch(ctx context.Context, req driver.Request) (*driver.Handle, error) {
	req.Brief = "task"
	req.Task.BaseCommit = "base"
	n := driver.Native{Binary: "/bin/sh", Args: func(driver.Request) []string { return []string{"-c", "echo test; sleep 30"} }, Parse: func([]byte) []marshal.CommandRecord { return nil }}
	return n.Launch(ctx, req)
}

func (m *testMockDriver) Wait(ctx context.Context, h *driver.Handle) (marshal.HandIn, error) {
	return marshal.HandIn{}, nil
}

func (m *testMockDriver) Cancel(h *driver.Handle) error {
	return (driver.Native{}).Cancel(h)
}

func TestDispatchedTaskWorkersTrackedAndStopped(t *testing.T) {
	_, logFile := setupFakeTmux(t)
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")

	workDir := t.TempDir()
	ws := NewWorkspace(nil, "test-proj", "test-session")
	ws.workDir = workDir
	cleanupTmuxWorkspace(t, ws)
	ws.InitTmux(workDir)
	ctx := context.Background()

	taskDrv := &tmuxTaskDriver{
		inner: &testMockDriver{},
		w:     ws,
	}

	req := driver.Request{
		Task: marshal.Task{
			PlanTaskID: "task-99",
			Worker:     "claude",
		},
		Worktree: workDir,
	}

	h, err := taskDrv.Launch(ctx, req)
	if err != nil {
		t.Fatalf("launch failed: %v", err)
	}
	if h == nil {
		t.Fatal("expected handle")
	}

	ws.tmuxMu.Lock()
	agent, ok := ws.tmuxActiveWins["task-task-99"]
	if !ok || agent == nil {
		t.Fatal("task worker was not tracked in tmuxActiveWins")
	}
	if agent.role != "task" {
		t.Fatalf("expected role 'task', got %q", agent.role)
	}
	if agent.taskID != "task-99" {
		t.Fatalf("expected taskID 'task-99', got %q", agent.taskID)
	}
	paneID := agent.paneID
	ws.tmuxMu.Unlock()

	// Stopping all workers should kill task workers as well
	resp := ws.StopAllWorkers(ctx)
	if !strings.Contains(resp, "Stopped all worker sessions") {
		t.Fatalf("unexpected StopAllWorkers response: %s", resp)
	}

	ws.tmuxMu.Lock()
	if _, ok := ws.tmuxActiveWins["task-task-99"]; ok {
		t.Fatal("task worker was NOT removed by StopAllWorkers")
	}
	ws.tmuxMu.Unlock()

	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logBytes)

	if !strings.Contains(logStr, "kill-pane -t "+paneID) {
		t.Fatalf("task window was not killed:\n%s", logStr)
	}
}

func TestF2TaskWindowRunsRealWorker(t *testing.T) {
	w := realTmuxWorkspace(t)
	d := &tmuxTaskDriver{w: w, inner: driver.Native{Provider: "test", Binary: "/bin/sh", Args: func(driver.Request) []string {
		return []string{"-c", "echo Recorded agent output evidence; read answer; echo fake-action:$answer"}
	}, Parse: func(data []byte) []marshal.CommandRecord {
		return []marshal.CommandRecord{{Command: "fake-action", Output: string(data)}}
	}}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h, err := d.Launch(ctx, realTaskRequest(t, w, "task-f2"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Cancel(h)
	w.tmuxMu.Lock()
	agent := w.tmuxActiveWins["task-task-f2"]
	w.tmuxMu.Unlock()
	if agent == nil || agent.role != "task" || !agent.readOnly || agent.paneID == "" {
		t.Fatalf("invalid task terminal: %+v", agent)
	}
	_, err = w.handleTakeoverCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if agent.readOnly {
		t.Fatal("takeover did not enable input")
	}
	if _, err = tmux.RunCommand(ctx, "send-keys", "-t", agent.paneID, "success", "Enter"); err != nil {
		t.Fatal(err)
	}
	handin, err := d.Wait(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(handin.RuntimeObserved) == 0 || !strings.Contains(handin.RuntimeObserved[0].Command, "/bin/sh") || !strings.Contains(handin.RuntimeObserved[0].Output, "Recorded agent output evidence") {
		t.Fatalf("missing real worker observations: %+v", handin)
	}
	if len(handin.WorkerReported) == 0 || handin.WorkerReported[0].Command != "fake-action" || !strings.Contains(handin.WorkerReported[0].Output, "fake-action:success") {
		t.Fatalf("missing parsed real worker output: %+v", handin)
	}
	evidence, err := os.ReadFile(filepath.Join(w.workDir, ".marshal", "evidence", "task-task-f2-latest.txt"))
	if err != nil || !strings.Contains(string(evidence), "Recorded agent output evidence") {
		t.Fatalf("missing terminal evidence: %q %v", evidence, err)
	}
	w.tmuxMu.Lock()
	_, exists := w.tmuxActiveWins["task-task-f2"]
	w.tmuxMu.Unlock()
	if exists {
		t.Fatal("completed task remains registered")
	}
	panes, err := tmux.ListPanes(ctx, w.tmuxSession)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range panes {
		if p.PaneID == agent.paneID {
			t.Fatal("completed terminal was not removed")
		}
	}
}

func TestF3MarshalChatAutoStartProtectedRestartResume(t *testing.T) {
	_, logFile, _ := setupFakeTmuxWithDeadFile(t)
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")

	workDir := t.TempDir()
	ctx := context.Background()

	ws := NewWorkspace(nil, "test-proj", "test-session")
	ws.workDir = workDir

	// 1. Initialize tmux: Marshal Chat MUST start automatically (F3)
	cleanupTmuxWorkspace(t, ws)
	ws.InitTmux(workDir)

	ws.tmuxMu.Lock()
	chatAgent, exists := ws.tmuxActiveWins["marshal-chat"]
	if !exists || chatAgent == nil {
		ws.tmuxMu.Unlock()
		t.Fatal("marshal-chat was not automatically started during InitTmux (F3)")
	}
	if chatAgent.role != "marshal-chat" {
		t.Fatalf("expected role 'marshal-chat', got %q", chatAgent.role)
	}
	if chatAgent.readOnly {
		t.Fatal("marshal-chat must have readOnly=false (operator input enabled normally) (F3)")
	}
	chatWin := chatAgent.window
	ws.tmuxMu.Unlock()

	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	logStr := string(logBytes)

	// Verify chat window was created and pane set to -e (normal input, not -d)
	if !strings.Contains(logStr, "new-window -t test-session -n "+chatWin) {
		t.Fatalf("missing chat new-window call:\n%s", logStr)
	}
	if !strings.Contains(logStr, "select-pane -t test-session:"+chatWin+" -e") {
		t.Fatalf("chat pane was not given operator input (-e):\n%s", logStr)
	}
	// Verify main MARSHAL window remains focused
	if !strings.Contains(logStr, "select-window -t test-session:@0") {
		t.Fatalf("marshal workspace was not re-selected after opening chat:\n%s", logStr)
	}

	// 2. Verify StopAllWorkers NEVER stops the Marshal chat (F3 & Decision 9)
	stopResp := ws.StopAllWorkers(ctx)
	if strings.Contains(stopResp, "Marshal Chat") {
		t.Fatalf("StopAllWorkers reported stopping Marshal Chat: %s", stopResp)
	}

	ws.tmuxMu.Lock()
	if _, ok := ws.tmuxActiveWins["marshal-chat"]; !ok {
		ws.tmuxMu.Unlock()
		t.Fatal("StopAllWorkers removed marshal-chat from active wins (F3 violation)")
	}
	ws.tmuxMu.Unlock()

	logBytes, _ = os.ReadFile(logFile)
	if strings.Contains(string(logBytes), "kill-window -t "+chatWin) {
		t.Fatalf("StopAllWorkers killed chat window %s (F3 violation)", chatWin)
	}

	// 3. Verify chat unexpected exit triggers automatic restart and conversation resume (F3)
	ws.tmuxMu.Lock()
	chatAgent.sessionID = "session-test-resume-456"
	chatAgent.binary = "codex"
	chatAgent.provider = "codex"
	ws.tmuxMu.Unlock()

	// Clear log file before restart to test respawn argv cleanly
	_ = os.WriteFile(logFile, []byte(""), 0o600)

	// Call restartMarshalChat
	ws.restartMarshalChat(ctx, chatAgent, workDir)

	ws.tmuxMu.Lock()
	restartedAgent, ok := ws.tmuxActiveWins["marshal-chat"]
	if !ok || restartedAgent == nil {
		ws.tmuxMu.Unlock()
		t.Fatal("marshal-chat missing after restart")
	}
	if restartedAgent.state != "working" {
		t.Fatalf("expected state 'working', got %q", restartedAgent.state)
	}
	if restartedAgent.readOnly {
		t.Fatal("restarted chat should remain input-enabled (readOnly=false)")
	}
	ws.tmuxMu.Unlock()

	restartLogs, _ := os.ReadFile(logFile)
	restartStr := string(restartLogs)

	// Verify respawn-window was called with resume command and conversation ID (F3)
	expectedRespawn := "respawn-window -k -t " + chatAgent.paneID + " env codex resume session-test-resume-456"
	if !strings.Contains(restartStr, expectedRespawn) {
		t.Fatalf("expected respawn with resume arguments %q, got:\n%s", expectedRespawn, restartStr)
	}

	// Verify input was re-enabled on the respawned window
	if !strings.Contains(restartStr, "select-pane -t "+chatAgent.paneID+" -e") {
		t.Fatalf("input was not enabled (-e) on respawned window:\n%s", restartStr)
	}
}

// Monitors must finish before temporary files and the process-global fake tmux
// binary are removed or replaced by the next test's private server.
func cleanupTmuxWorkspace(t *testing.T, w *Workspace) {
	t.Helper()
	t.Cleanup(func() {
		w.tmuxMu.Lock()
		agents := make([]*activeTmuxAgent, 0, len(w.tmuxActiveWins))
		for _, a := range w.tmuxActiveWins {
			agents = append(agents, copyAgentLocked(a))
		}
		w.tmuxMu.Unlock()
		for _, a := range agents {
			if a.cancel != nil {
				a.cancel()
			}
			if a.handle != nil && a.driver != nil {
				_ = a.driver.Cancel(a.handle)
			}
		}
		w.tmuxMonitors.Wait()
	})
}
