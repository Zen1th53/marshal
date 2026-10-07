package tui

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tmux"
)

// Exercise the same terminal host used by operator sessions and dispatched
// workers without needing a live relay listener or provider process.
func TestNativeTmuxHostInputOrigin(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "opencode", "antigravity"} {
		for _, origin := range []nativeLaunchOrigin{nativeLaunchOperator, nativeLaunchAutomated} {
			t.Run(provider+"/"+string(origin), func(t *testing.T) {
				_, logFile := setupFakeTmux(t)
				root := t.TempDir()
				w := NewWorkspace(nil, "project", "session")
				w.workDir, w.tmuxSession = root, "test-session"
				name := tmux.WindowName(provider, root)
				if err := w.hostNativeTmuxWindow(context.Background(), name, root, "unused-socket", origin); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(logFile)
				if err != nil {
					t.Fatal(err)
				}
				flag, forbidden := "-e", "-d"
				if origin == nativeLaunchAutomated {
					flag, forbidden = forbidden, flag
				}
				prefix := "select-pane -t test-session:" + name + " "
				if !strings.Contains(string(data), prefix+flag+"\n") || strings.Contains(string(data), prefix+forbidden+"\n") {
					t.Fatalf("incorrect input state for %s:\n%s", origin, data)
				}
				if origin == nativeLaunchAutomated {
					t.Setenv("TMUX", "test")
					w.tmuxPath = "fake"
					w.tmuxActiveWins = map[string]*activeTmuxAgent{provider: {id: provider, role: "task", provider: provider, paneID: "%1", readOnly: true}}
					w.nativeProvider = provider
					if _, err := w.handleTakeoverCommand(context.Background()); err != nil {
						t.Fatal(err)
					}
					data, _ = os.ReadFile(logFile)
					if !strings.Contains(string(data), "select-pane -t %1 -e\n") || w.tmuxActiveWins[provider].readOnly {
						t.Fatalf("takeover did not enable input:\n%s", data)
					}
				}
			})
		}
	}
}

func TestNativeTmuxLaunchOriginRecovery(t *testing.T) {
	_, _ = setupFakeTmux(t)
	root := t.TempDir()
	w := NewWorkspace(nil, "project", "session")
	w.workDir, w.tmuxSession = root, "test-session"
	cleanupTmuxWorkspace(t, w)
	for _, tc := range []struct {
		id       string
		origin   nativeLaunchOrigin
		readOnly bool
	}{
		{"codex", nativeLaunchOperator, false},
		{"task-1", nativeLaunchAutomated, true},
		{"legacy-worker", "", true},
	} {
		name := tmux.WindowName(tc.id, root)
		if err := tmux.NewWindow(context.Background(), w.tmuxSession, name, root, nil, []string{"echo"}); err != nil {
			t.Fatal(err)
		}
		panes, err := tmux.ListPanes(context.Background(), w.tmuxSession)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range panes {
			if p.WindowName == name {
				a := &activeTmuxAgent{id: tc.id, role: "worker", provider: "codex", paneID: p.PaneID, window: name, launchOrigin: tc.origin}
				if err := saveAgentRecord(root, w.tmuxSession, a); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	w.adoptSurvivingWorkers(root)
	w.tmuxMu.Lock()
	defer w.tmuxMu.Unlock()
	for id, want := range map[string]bool{"codex": false, "task-1": true, "legacy-worker": true} {
		a := w.tmuxActiveWins[id]
		if a == nil || a.readOnly != want {
			t.Fatalf("recovered %s input state: %+v, want readOnly %v", id, a, want)
		}
	}
}
