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
