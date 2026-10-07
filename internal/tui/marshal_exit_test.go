package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestMarshalChatAndNativeExitReturnOnlyViewedWindow(t *testing.T) {
	for _, chat := range []bool{true, false} {
		for _, viewed := range []bool{true, false} {
			t.Run(fmt.Sprintf("chat=%v/viewed=%v", chat, viewed), func(t *testing.T) {
				root := t.TempDir()
				log := filepath.Join(root, "calls")
				selected := filepath.Join(root, "selected")
				fake := filepath.Join(root, "tmux")
				active := "0"
				if viewed {
					active = "1"
				}
				script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
case "$1" in
 display-message)
  case "$*" in
   *'#{pane_dead}'*) echo '1 0' ;;
   *'#{session_name}:#{window_id}'*) echo 'session:@0' ;;
   *'#{session_id}'*) echo '$0' ;;
   *'#{window_active}'*) echo %s ;;
   *'#{pane_id}'*) echo '%%1' ;;
  esac ;;
 if-shell)
  [ "$5" = '#{window_active}' ] || exit 0
  if [ %s = 1 ]; then printf '%%s\n' "$6" > %q; fi ;;
 list-panes) printf '%%%%1\t@1\tchat\t0\t1\t0\n' ;;
 respawn-window) touch %q ;;
 capture-pane) echo 'saved output' ;;
esac
`, log, active, active, selected, filepath.Join(root, "restarted"))
				if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				tmux.SetBinaryPath(fake)
				defer tmux.ResetBinaryPath()
				w := NewWorkspace(nil, "project", "session")
				defer w.Close()
				w.workDir = root
				w.tmuxSession, w.tmuxMarshalWinID, w.tmuxMarshalPaneID = "session", "@0", "%0"
				a := &activeTmuxAgent{id: "codex", provider: "codex", label: "Codex", paneID: "%1", windowID: "@1", window: "chat", launchOrigin: nativeLaunchOperator, doneChan: make(chan struct{}), binary: "/bin/true"}
				if chat {
					a.id, a.role, a.label = "marshal-chat", "marshal-chat", "Marshal Chat"
				}
				w.tmuxActiveWins[a.id] = a
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				w.monitorAgent(ctx, a, root, nil, nil, nil, nil, nil)
				deadline := time.Now().Add(5 * time.Second)
				for {
					w.mu.RLock()
					out := w.state.LastOutput
					w.mu.RUnlock()
					done := strings.Contains(out, "session ended")
					if chat {
						_, err := os.Stat(filepath.Join(root, "restarted"))
						done = err == nil
					}
					if done {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("no exit outcome: %s", out)
					}
					time.Sleep(10 * time.Millisecond)
				}
				cancel()
				<-a.doneChan
				data, err := os.ReadFile(selected)
				if viewed && (err != nil || !strings.Contains(string(data), "select-window") || !strings.Contains(string(data), "@0")) {
					t.Fatalf("did not return to control centre: %q %v", data, err)
				}
				if !viewed && !os.IsNotExist(err) {
					t.Fatalf("stole focus: %q %v", data, err)
				}
				if chat {
					w.mu.RLock()
					out := w.state.LastOutput
					w.mu.RUnlock()
					if strings.Count(out, "Marshal chat ended") != 1 || !strings.Contains(out, "restarting automatically") || !strings.Contains(out, "/marshal chat") {
						t.Fatalf("exit guidance: %s", out)
					}
				}
			})
		}
	}
}
