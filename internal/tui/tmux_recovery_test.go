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

	"github.com/Zen1th53/marshal/internal/processgroup"
	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestRecoveredWorkersRetainNaturalCompletion(t *testing.T) {
	for _, code := range []string{"0", "7"} {
		t.Run(code, func(t *testing.T) {
			w := realTmuxWorkspace(t)
			gate := filepath.Join(w.workDir, "finish")
			_, err := w.runNativeAgentInTmux(context.Background(), "test", "Test", w.workDir, "/bin/sh", []string{"-c", "echo RECOVERED_OUTPUT; while [ ! -f finish ]; do sleep .02; done; exit " + code}, nil, nil, nil, nil, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			w.tmuxMu.Lock()
			a := w.tmuxActiveWins["test"]
			w.tmuxMu.Unlock()
			defer a.driver.Cancel(a.handle)
			a.cancel()
			<-a.doneChan
			recovered := NewWorkspace(nil, "project", "session")
			recovered.workDir, recovered.tmuxSession, recovered.tmuxPath = w.workDir, w.tmuxSession, w.tmuxPath
			recovered.tmuxMarshalWin = w.tmuxMarshalWin
			recovered.tmuxMu.Lock()
			recovered.adoptSurvivingWorkersLocked(w.workDir)
			adopted := recovered.tmuxActiveWins["test"]
			recovered.tmuxMu.Unlock()
			if adopted == nil {
				t.Fatal("worker not adopted")
			}
			defer func() { adopted.cancel(); <-adopted.doneChan }()
			if err := os.WriteFile(gate, nil, 0600); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(4 * time.Second)
			for {
				recovered.tmuxMu.Lock()
				_, active := recovered.tmuxActiveWins["test"]
				recovered.tmuxMu.Unlock()
				if !active {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("recovered worker completion was not retained")
				}
				time.Sleep(10 * time.Millisecond)
			}
			record, err := loadAgentRecord(w.workDir, a.paneID)
			want := "done"
			if code != "0" {
				want = "failed"
			}
			if err != nil || record.Outcome != want {
				t.Fatalf("outcome = %q, %v; want %s", record.Outcome, err, want)
			}
			data, err := os.ReadFile(filepath.Join(w.workDir, ".marshal", "evidence", "test-latest.txt"))
			if err != nil || !strings.Contains(string(data), "RECOVERED_OUTPUT") {
				t.Fatalf("missing retained output: %s %v", data, err)
			}
			panes, err := tmux.ListPanes(context.Background(), w.tmuxSession)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range panes {
				if p.PaneID == a.paneID {
					t.Fatal("completed pane still hosted")
				}
			}
		})
	}
}

func TestNativeHostOptionFailuresAbortLaunch(t *testing.T) {
	for _, option := range []string{"remain-on-exit", "select-pane"} {
		t.Run(option, func(t *testing.T) {
			bin, log, _ := setupFakeTmuxWithDeadFile(t)
			original := bin + "-original"
			if err := os.Rename(bin, original); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\ncase \"$1\" in set-option|select-pane) case \"$*\" in *" + option + "*) exit 1;; esac;; esac\nexec " + original + " \"$@\"\n"
			if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			w := NewWorkspace(nil, "project", "session")
			w.workDir = t.TempDir()
			w.tmuxSession = "test-session"
			w.tmuxPath = bin
			if _, err := w.runNativeAgentInTmux(context.Background(), "test", "Test", w.workDir, "/bin/sh", []string{"-c", "touch worker-started; sleep 30"}, nil, nil, nil, nil, nil, nil, nil); err == nil {
				t.Fatal("host option failure was ignored")
			}
			if len(w.tmuxActiveWins) != 0 {
				t.Fatal("failed host was registered")
			}
			if _, err := os.Stat(filepath.Join(w.workDir, "worker-started")); !os.IsNotExist(err) {
				t.Fatalf("worker started despite failed host: %v", err)
			}
			data, err := os.ReadFile(log)
			if err != nil || !strings.Contains(string(data), "kill-pane") {
				t.Fatalf("failed host was not removed: %s %v", data, err)
			}
		})
	}
}

// The supervisor can be reaped before the driver publishes its durable result.
// Its terminal relay stays alive until that publication has finished.
func TestRecoveredWorkerWaitsForOutcomeWhileRelayIsAlive(t *testing.T) {
	bin, log, _ := setupFakeTmuxWithDeadFile(t)
	w := NewWorkspace(nil, "project", "session")
	w.workDir, w.tmuxSession, w.tmuxPath = t.TempDir(), "test-session", bin
	cleanupTmuxWorkspace(t, w)
	supervisor := exec.Command("sleep", "30")
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	ref, err := processgroup.Identify(supervisor)
	_ = supervisor.Process.Kill()
	_ = supervisor.Wait()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a := &activeTmuxAgent{id: "test", role: "worker", paneID: "%0", supervisor: ref, state: "working", cancel: cancel, doneChan: make(chan struct{})}
	w.tmuxActiveWins[a.id] = a
	if err := saveAgentRecord(w.workDir, w.tmuxSession, a); err != nil {
		t.Fatal(err)
	}
	w.monitorAgent(ctx, a, w.workDir, nil, nil, nil, nil, nil)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-a.doneChan:
			t.Fatal("recovered worker closed before the driver published its outcome")
		case <-ctx.Done():
			t.Fatal("monitor did not poll the live relay twice")
		case <-ticker.C:
		}
		data, _ := os.ReadFile(log)
		if strings.Count(string(data), "#{pane_dead}") >= 2 {
			break
		}
	}
	w.tmuxMu.Lock()
	state := a.state
	w.tmuxMu.Unlock()
	if state != "working" {
		t.Fatalf("unpublished outcome classified as %s", state)
	}
	if err := agentCompletion(w.workDir, a.id)(0); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.doneChan:
	case <-ctx.Done():
		t.Fatal("published completion was not retained")
	}
	record, err := loadAgentRecord(w.workDir, a.paneID)
	if err != nil || record.Outcome != "done" {
		t.Fatalf("outcome = %q, %v; want done", record.Outcome, err)
	}
}
