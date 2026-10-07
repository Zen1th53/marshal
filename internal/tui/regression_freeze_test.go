//go:build linux

package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestSafetyCommandsBypassBusyLane(t *testing.T) {
	for _, command := range []string{"/stop all", "/takeover", "/cancel", "/focus"} {
		t.Run(command, func(t *testing.T) {
			w := NewWorkspace(nil, "test", "test")
			w.workDir = t.TempDir()
			w.uiEvents = make(chan func(), 64)
			w.commandBusy.Store(true)
			w.runCommand(context.Background(), command)
			deadline := time.After(3 * time.Second)
			for {
				select {
				case fn := <-w.uiEvents:
					fn()
					if strings.Contains(w.GetUIState().LastOutput, "still running") {
						t.Fatal("safety action refused")
					}
					if !w.commandBusy.Load() {
						t.Fatal("safety action released background ownership")
					}
					w.commandWG.Wait()
					return
				case <-deadline:
					t.Fatalf("%s did not finish independently: %#v", command, w.GetUIState())
				}
			}
		})
	}
}

func TestAdoptSurvivingWorkersLockedContract(t *testing.T) {
	path, _ := setupFakeTmux(t)
	w := NewWorkspace(nil, "test", "test")
	w.workDir = t.TempDir()
	w.tmuxPath, w.tmuxSession = path, "test-session"
	done := make(chan struct{})
	go func() {
		w.tmuxMu.Lock()
		w.adoptSurvivingWorkers(w.workDir)
		w.tmuxMu.Unlock()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("recovery reentered caller's tmuxMu")
	}
}

func TestTakeoverSelectsViewedWorkerAndRejectsOperators(t *testing.T) {
	path, log := setupFakeTmux(t)
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")
	w := NewWorkspace(nil, "test", "test")
	w.workDir = t.TempDir()
	w.tmuxPath, w.tmuxSession = path, "test-session"
	w.nativeProvider = "codex"
	w.tmuxActiveWins["codex"] = &activeTmuxAgent{id: "codex", provider: "codex", paneID: "%1", launchOrigin: nativeLaunchOperator}
	w.tmuxActiveWins["task"] = &activeTmuxAgent{id: "task", role: "task", paneID: "%2", readOnly: true, launchOrigin: nativeLaunchAutomated}
	msg, err := w.handleTakeoverCommand(context.Background())
	if err != nil || !strings.Contains(msg, "Takeover:") {
		t.Fatalf("%s: %v", msg, err)
	}
	data, _ := os.ReadFile(log)
	if !strings.Contains(string(data), "select-pane -t %2 -e") || strings.Contains(string(data), "select-pane -t %1 -e") {
		t.Fatalf("wrong pane enabled: %s", data)
	}
	msg, err = w.handleTakeoverCommand(context.Background(), "codex")
	if err == nil {
		t.Fatalf("operator accepted: %s", msg)
	}
}

func TestMissingPaneRetiredPreservingEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tmux")
	script := "#!/bin/sh\ncase \"$1\" in display-message) exit 1;; list-panes) printf '%%0\\t@0\\tmarshal\\t100\\t0\\t0\\t\\n';; esac\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(path)
	t.Cleanup(tmux.ResetBinaryPath)
	w := NewWorkspace(nil, "test", "test")
	w.workDir, w.tmuxSession = t.TempDir(), "test"
	a := &activeTmuxAgent{id: "task", role: "task", paneID: "%9"}
	w.tmuxActiveWins[a.id] = a
	if err := saveAgentEvidence(w.workDir, a.id, "earlier evidence"); err != nil {
		t.Fatal(err)
	}
	if err := w.retainAndCloseAgent(context.Background(), a, w.workDir, "closed externally"); err != nil {
		t.Fatal(err)
	}
	if w.tmuxActiveWins[a.id] != nil || !a.cleaned {
		t.Fatal("missing pane still monitored")
	}
	if err := w.retainAndCloseAgent(context.Background(), a, w.workDir, "closed externally"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(w.workDir, ".marshal", "evidence", "task-latest.txt"))
	if err != nil || !strings.Contains(string(data), "earlier evidence") {
		t.Fatalf("earlier evidence lost: %q %v", data, err)
	}
}

func TestTakeoverViewedWorkerWinsAndAmbiguityRequiresAgent(t *testing.T) {
	w := realTmuxWorkspace(t)
	ctx := context.Background()
	for _, id := range []string{"first", "viewed"} {
		if _, err := w.runNativeAgentInTmux(ctx, nativeLaunchAutomated, id, id, w.workDir, "/bin/sh", []string{"-c", "read answer"}, nil, nil, nil, nil, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	w.tmuxMu.Lock()
	first, viewed := copyAgentLocked(w.tmuxActiveWins["first"]), copyAgentLocked(w.tmuxActiveWins["viewed"])
	w.nativeProvider = "first"
	w.tmuxMu.Unlock()
	if _, err := w.cmd.Handle(ctx, "/takeover"); err != nil {
		t.Fatal(err)
	}
	assertNativePaneInput(t, ctx, viewed.paneID, "0")
	assertNativePaneInput(t, ctx, first.paneID, "1")
	if err := tmux.SetPaneReadOnly(ctx, viewed.paneID, true); err != nil {
		t.Fatal(err)
	}
	w.tmuxMu.Lock()
	w.tmuxActiveWins["viewed"].readOnly = true
	w.tmuxMu.Unlock()
	if err := tmux.SelectWindow(ctx, w.marshalTarget()); err != nil {
		t.Fatal(err)
	}
	if msg, err := w.cmd.Handle(ctx, "/takeover"); err == nil || !strings.Contains(err.Error(), "/takeover <agent>") {
		t.Fatalf("ambiguous workers accepted: %s %v", msg, err)
	}
	if _, err := w.cmd.Handle(ctx, "/takeover first"); err != nil {
		t.Fatal(err)
	}
	assertNativePaneInput(t, ctx, first.paneID, "0")
	assertNativePaneInput(t, ctx, viewed.paneID, "1")
}

func TestStopAllBusyLaneStopsRealWorker(t *testing.T) {
	w := realTmuxWorkspace(t)
	ctx := context.Background()
	if _, err := w.runNativeAgentInTmux(ctx, nativeLaunchAutomated, "worker", "Worker", w.workDir, "/bin/sh", []string{"-c", "read answer"}, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	w.uiEvents = make(chan func(), 64)
	w.commandBusy.Store(true)
	w.runCommand(ctx, "/stop all")
	deadline := time.After(3 * time.Second)
	for {
		select {
		case fn := <-w.uiEvents:
			fn()
			if strings.Contains(w.GetUIState().LastOutput, "Stopped all worker sessions") {
				w.commandWG.Wait()
				if !w.commandBusy.Load() {
					t.Fatal("ordinary lane ownership lost")
				}
				w.tmuxMu.Lock()
				remaining := len(w.tmuxActiveWins)
				w.tmuxMu.Unlock()
				if remaining != 0 {
					t.Fatal("worker was not stopped")
				}
				return
			}
		case <-deadline:
			t.Fatalf("stop-all did not bypass lane: %s", w.GetUIState().LastOutput)
		}
	}
}

func TestClosedPaneMonitorRetiresOnce(t *testing.T) {
	w := realTmuxWorkspace(t)
	ctx := context.Background()
	if _, err := w.runNativeAgentInTmux(ctx, nativeLaunchOperator, "worker", "Worker", w.workDir, "/bin/sh", []string{"-c", "read answer"}, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	w.tmuxMu.Lock()
	a := w.tmuxActiveWins["worker"]
	pane := a.paneID
	w.tmuxMu.Unlock()
	defer a.driver.Cancel(a.handle)
	if err := saveAgentEvidence(w.workDir, a.id, "earlier evidence"); err != nil {
		t.Fatal(err)
	}
	if err := tmux.KillPane(ctx, pane); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.doneChan:
	case <-time.After(3 * time.Second):
		t.Fatal("closed pane monitor did not stop")
	}
	status, statusErr := tmux.RunCommand(ctx, "show-options", "-w", "-v", "-t", w.marshalTarget(), "@marshal_status")
	if statusErr != nil || strings.Contains(string(status), "Worker: working") {
		t.Fatalf("retired pane left stale status: %q %v", status, statusErr)
	}
	w.tmuxMu.Lock()
	remaining := w.tmuxActiveWins[a.id]
	w.tmuxMu.Unlock()
	if remaining != nil {
		t.Fatal("closed pane record retained")
	}
	output := w.GetUIState().LastOutput
	if strings.Count(output, "pane closed externally") != 1 {
		t.Fatalf("expected one retirement message: %s", output)
	}
	if err := w.retainAndCloseAgent(ctx, a, w.workDir, "closed externally"); err != nil {
		t.Fatal(err)
	}
	if w.GetUIState().LastOutput != output {
		t.Fatal("retirement repeated")
	}
	data, err := os.ReadFile(filepath.Join(w.workDir, ".marshal", "evidence", "worker-latest.txt"))
	if err != nil || string(data) != "earlier evidence" {
		t.Fatalf("lost evidence: %q %v", data, err)
	}
}

func TestRelayHostUsesCreationPaneAfterRename(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "tmux")
	log := bin + ".log"
	// The name is unavailable immediately after creation; only %77 resolves.
	script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\ncase \"$1\" in\nnew-window) printf '%%77\\n';;\ndisplay-message) case \"$*\" in *'%77'*) printf '%%77\\n';; *) exit 1;; esac;;\n*) case \"$*\" in *' -t %77 '*) exit 0;; *) exit 1;; esac;;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(bin)
	t.Cleanup(tmux.ResetBinaryPath)
	w := NewWorkspace(nil, "test", "test")
	w.tmuxSession = "test-session"
	var pane string
	if err := w.hostNativeTmuxWindow(context.Background(), "gone-name", t.TempDir(), "unused", nativeLaunchAutomated, &pane); err != nil {
		t.Fatal(err)
	}
	if pane != "%77" {
		t.Fatalf("wrong identity: %s", pane)
	}
	data, _ := os.ReadFile(log)
	if !strings.Contains(string(data), "select-pane -t %77 -d") || strings.Contains(string(data), "-t test-session:gone-name") {
		t.Fatalf("name used after creation: %s", data)
	}
}

func TestReturnKeyInstalledBeforeSelectingWorker(t *testing.T) {
	_, log := setupFakeTmux(t)
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")
	w := NewWorkspace(nil, "test", "test")
	w.workDir = t.TempDir()
	w.tmuxSession = "test-session"
	cleanupTmuxWorkspace(t, w)
	if _, err := w.runNativeAgentInTmux(context.Background(), nativeLaunchOperator, "test", "Test", w.workDir, "/bin/sh", []string{"-c", "read answer"}, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(log)
	lines := strings.Split(string(data), "\n")
	bound := false
	for _, line := range lines {
		if strings.HasPrefix(line, "bind-key ") && strings.Contains(line, " F11 ") {
			bound = true
		}
		if strings.HasPrefix(line, "select-window ") && !bound {
			t.Fatalf("worker exposed before return key: %s", data)
		}
	}
	if !bound {
		t.Fatal("return key absent")
	}
}
