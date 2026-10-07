package tui

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestProjectsKeepRootBindingsAndTargetOwnWindows(t *testing.T) {
	w := realTmuxWorkspace(t)
	ctx := context.Background()
	before, err := tmux.RunCommand(ctx, "list-keys", "-T", "root")
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.runNativeAgentInTmux(ctx, nativeLaunchOperator, "test", "Test", w.workDir, "/bin/sh", []string{"-c", "read answer"}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	second := NewWorkspace(nil, "second", "session")
	second.workDir = t.TempDir()
	cleanupTmuxWorkspace(t, second)
	second.tmuxSession = w.tmuxSession
	second.tmuxPath = w.tmuxPath
	if err := tmux.NewWindow(ctx, w.tmuxSession, "second-marshal", second.workDir, nil, []string{"sleep", "30"}); err != nil {
		t.Fatal(err)
	}
	second.tmuxMarshalWin = "second-marshal"
	_, err = second.runNativeAgentInTmux(ctx, nativeLaunchOperator, "test", "Test", second.workDir, "/bin/sh", []string{"-c", "read answer"}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := tmux.RunCommand(ctx, "list-keys", "-T", "root")
	if string(before) != string(after) {
		t.Fatal("opening projects changed server-global root bindings")
	}
	firstKeys, err := tmux.RunCommand(ctx, "list-keys", "-T", "marshal-keys-"+tmux.ProjectHash(w.workDir))
	if err != nil {
		t.Fatal(err)
	}
	secondKeys, err := tmux.RunCommand(ctx, "list-keys", "-T", "marshal-keys-"+tmux.ProjectHash(second.workDir))
	if err != nil {
		t.Fatal(err)
	}
	if string(firstKeys) == string(secondKeys) {
		t.Fatal("project key tables collided")
	}
	if strings.Contains(string(firstKeys), "-t marshal") || strings.Contains(string(secondKeys), "-t marshal") {
		t.Fatal("bindings use ambiguous bare window targets")
	}
	w.StopAllWorkers(ctx)
	secondAfter, err := tmux.RunCommand(ctx, "list-keys", "-T", "marshal-keys-"+tmux.ProjectHash(second.workDir))
	if err != nil || string(secondKeys) != string(secondAfter) {
		t.Fatal("ending first project altered second project's keys")
	}
	w.tmuxMu.Lock()
	w.tmuxAlerts = map[string]string{"one": "first-project-alert"}
	w.tmuxMu.Unlock()
	second.tmuxMu.Lock()
	second.tmuxAlerts = map[string]string{"two": "second-project-alert"}
	second.tmuxMu.Unlock()
	w.updateTmuxStatusLine(ctx)
	second.updateTmuxStatusLine(ctx)
	firstStatus, err := tmux.RunCommand(ctx, "show-options", "-w", "-v", "-t", w.tmuxMarshalWin, "@marshal_status")
	if err != nil {
		t.Fatal(err)
	}
	secondStatus, err := tmux.RunCommand(ctx, "show-options", "-w", "-v", "-t", w.tmuxSession+":second-marshal", "@marshal_status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(firstStatus), "first-project-alert") || strings.Contains(string(firstStatus), "second-project-alert") || !strings.Contains(string(secondStatus), "second-project-alert") {
		t.Fatal("project status projections collided")
	}
	second.StopAllWorkers(ctx)
	final, _ := tmux.RunCommand(ctx, "list-keys", "-T", "root")
	if string(before) != string(final) {
		t.Fatal("cleanup globally unbound keys")
	}
}

func TestRealClientReturnKeyAndInputStayWindowScoped(t *testing.T) {
	w := realTmuxWorkspace(t)
	ctx := context.Background()
	// Preserve a user's nonstandard default table outside MARSHAL panes.
	if _, err := tmux.RunCommand(ctx, "bind-key", "-T", "user-root", "F12", "send-keys", "user-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := tmux.RunCommand(ctx, "set-option", "-t", w.tmuxSession, "key-table", "user-root"); err != nil {
		t.Fatal(err)
	}
	// The TUI owns this terminal, but no other window in the user's session.
	panes, _ := tmux.ListPanes(ctx, w.tmuxSession)
	w.tmuxMarshalPaneID = panes[0].PaneID
	// Match InitTmux: the control centre also owns the project key table.
	bindErr := w.bindWorkspaceKeys(ctx, w.tmuxMarshalPaneID, w.workDir)
	if bindErr != nil {
		t.Fatal(bindErr)
	}
	_, err := w.runNativeAgentInTmux(ctx, nativeLaunchOperator, "codex", "Codex", w.workDir, "/bin/sh", []string{"-c", "echo READY; while read line; do echo received:$line; done"}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Detached takeover selects the worker for the next client to attach.
	if _, err := w.handleTakeoverCommand(ctx); err != nil {
		t.Fatal(err)
	}
	master, slave := openPTY(t)
	setWinsize(slave, 30, 100)
	client := exec.Command(w.tmuxPath, "attach-session", "-t", w.tmuxSession)
	client.Stdin, client.Stdout, client.Stderr = slave, slave, slave
	client.Env = append(os.Environ(), "TERM=xterm-256color")
	client.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { client.Process.Kill(); client.Wait(); w.StopAllWorkers(ctx) }()
	go io.Copy(io.Discard, master)
	// Wait for attachment, then assert the initial input state immediately.
	attachedDeadline := time.Now().Add(time.Second)
	for {
		clients, err := tmux.RunCommand(ctx, "list-clients", "-t", w.tmuxSession, "-F", "#{client_name}")
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(clients)) != "" {
			break
		}
		if time.Now().After(attachedDeadline) {
			t.Fatal("tmux client did not attach")
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, err := tmux.RunCommand(ctx, "list-clients", "-t", w.tmuxSession, "-F", "#{pane_input_off} #{client_key_table}")
	if err != nil {
		t.Fatal(err)
	}
	if want := "0 marshal-keys-" + tmux.ProjectHash(w.workDir); strings.TrimSpace(string(state)) != want {
		t.Fatalf("client attached after takeover with wrong input state: got %q, want %q", state, want)
	}
	w.tmuxMu.Lock()
	a := copyAgentLocked(w.tmuxActiveWins["codex"])
	w.tmuxMu.Unlock()
	// Activate the window hook in a real attached client.
	if err := tmux.SelectWindow(ctx, w.tmuxMarshalPaneID); err != nil {
		t.Fatal(err)
	}
	if _, err := master.Write([]byte("\x1b[18~")); err != nil {
		t.Fatal(err)
	} // F7 on xterm
	focusDeadline := time.Now().Add(time.Second)
	for {
		selected, err := tmux.RunCommand(ctx, "display-message", "-p", "-t", w.tmuxSession, "#{pane_id}")
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(selected)) == a.paneID {
			break
		}
		if time.Now().After(focusDeadline) {
			t.Fatalf("explicit F7 did not select Codex: %q", selected)
		}
		time.Sleep(10 * time.Millisecond)
	}
	master.Write([]byte("\x1b[23~")) // F11 on xterm
	deadline := time.Now().Add(time.Second)
	for {
		selected, _ := tmux.RunCommand(ctx, "display-message", "-p", "-t", w.tmuxSession, "#{pane_id}")
		if strings.TrimSpace(string(selected)) == w.tmuxMarshalPaneID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("F11 did not return to Marshal: %q", selected)
		}
		time.Sleep(10 * time.Millisecond)
	}
	takeoverAndType := func(line string) {
		t.Helper()
		if _, err := w.handleTakeoverCommand(ctx); err != nil {
			t.Fatal(err)
		}
		// A successful take-over must leave the attached client ready immediately.
		state, err := tmux.RunCommand(ctx, "list-clients", "-t", w.tmuxSession, "-F", "#{pane_id} #{pane_input_off} #{pane_in_mode} #{client_key_table}")
		if err != nil {
			t.Fatal(err)
		}
		want := a.paneID + " 0 0 marshal-keys-" + tmux.ProjectHash(w.workDir)
		if strings.TrimSpace(string(state)) != want {
			t.Fatalf("take over returned before client input was ready: got %q, want %q", state, want)
		}
		if _, err := master.Write([]byte(line + "\r")); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(time.Second)
		for {
			out, _ := tmux.CapturePane(ctx, a.paneID)
			if strings.Contains(out, "received:"+line) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("private table lost worker input: %q", out)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	takeoverAndType("hello")
	// An already focused joined pane does not fire after-select-pane. A
	// pending prefix table must not survive a successful take-over.
	if err := tmux.JoinPane(ctx, a.paneID, w.tmuxMarshalPaneID, true); err != nil {
		t.Fatal(err)
	}
	w.tmuxMu.Lock()
	w.tmuxActiveWins["codex"].isJoined = true
	w.tmuxMu.Unlock()
	if err := tmux.SelectPane(ctx, a.paneID); err != nil {
		t.Fatal(err)
	}
	if _, err := tmux.RunCommand(ctx, "copy-mode", "-t", a.paneID); err != nil {
		t.Fatal(err)
	}
	if _, err := tmux.RunCommand(ctx, "switch-client", "-T", "prefix"); err != nil {
		t.Fatal(err)
	}
	// Also exercise every byte through key dispatch, without paste detection.
	if _, err := tmux.RunCommand(ctx, "set-option", "-t", w.tmuxSession, "assume-paste-time", "0"); err != nil {
		t.Fatal(err)
	}
	takeoverAndType("hello-again")
	takeoverAndType("héllo-世界")
	// Leaving an owned pane must restore the original table and all input.
	if err := tmux.NewWindow(ctx, w.tmuxSession, "unrelated", w.workDir, nil, []string{"/bin/sh", "-c", "while read line; do echo unrelated:$line; done"}); err != nil {
		t.Fatal(err)
	}
	if err := tmux.SelectWindow(ctx, w.tmuxSession+":unrelated"); err != nil {
		t.Fatal(err)
	}
	state, err = tmux.RunCommand(ctx, "list-clients", "-t", w.tmuxSession, "-F", "#{client_key_table}")
	if err != nil || strings.TrimSpace(string(state)) != "user-root" {
		t.Fatalf("unrelated window lost user table: %q, %v", state, err)
	}
	if _, err := master.Write([]byte("hello\r\x1b[24~\r")); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for {
		out, _ := tmux.CapturePane(ctx, w.tmuxSession+":unrelated")
		if strings.Contains(out, "unrelated:hello") && strings.Contains(out, "unrelated:user-key") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("unrelated window lost input: %q", out)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStatusProjectionDoesNotInitializeChat(t *testing.T) {
	_, log := setupFakeTmux(t)
	w := NewWorkspace(nil, "project", "session")
	w.workDir = t.TempDir()
	w.updateTmuxStatusLine(context.Background())
	if len(w.tmuxActiveWins) != 0 {
		t.Fatal("status projection started an unowned monitor")
	}
	data, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("status projection initialized tmux: %s", data)
	}
}

func TestInactiveTmuxDoesNotInitializeChat(t *testing.T) {
	_, log := setupFakeTmux(t)
	t.Setenv("TMUX", "")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "")
	w := NewWorkspace(nil, "project", "session")
	w.workDir = t.TempDir()
	cleanupTmuxWorkspace(t, w)
	if w.isTmuxActive() {
		t.Fatal("reported an active tmux connection outside tmux")
	}
	w.tmuxMu.Lock()
	agents := len(w.tmuxActiveWins)
	w.tmuxMu.Unlock()
	data, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if agents != 0 || len(data) != 0 {
		t.Fatalf("inactive check initialized tmux: agents=%d commands=%s", agents, data)
	}
}
