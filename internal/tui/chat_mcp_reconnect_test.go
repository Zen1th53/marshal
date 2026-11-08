package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestChatMCPInPlaceResume(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		for _, fail := range []bool{false, true} {
			name := provider + "/success"
			if fail {
				name = provider + "/respawn-failure"
			}
			t.Run(name, func(t *testing.T) {
				bin, log := setupFakeTmux(t)
				marshalSwitchEnvironment(t)
				w := NewWorkspace(nil, "project", "runtime")
				w.workDir = t.TempDir()
				root := w.providerRoot()
				w.tmuxSession = fixtureTmuxNamespace(t, root)
				w.tmuxPath = bin
				a := &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", provider: provider, paneID: "%1", sessionID: "keep-this-conversation", window: tmux.ChatWindowName(root), state: "working", env: []string{"MARSHAL_PROPOSAL_MCP_CONFIG=stale-config"}}
				w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": a}
				if fail {
					script, err := os.ReadFile(bin)
					if err != nil {
						t.Fatal(err)
					}
					script = []byte(strings.Replace(string(script), "respawn-window|respawn-pane)\n    exit 0", "respawn-window|respawn-pane)\n    exit 1", 1))
					if err := os.WriteFile(bin, script, 0755); err != nil {
						t.Fatal(err)
					}
				}
				err := w.respawnMarshalChat(t.Context(), a, root, false)
				if fail {
					if err == nil {
						t.Fatal("failed respawn was hidden")
					}
					w.chatMCPRecoveryFailed(root, err)
					if !strings.Contains(w.GetUIState().LastOutput, "/marshal chat") {
						t.Fatal("repair instruction missing")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(a.briefingDir.remove)
					binding := loadChatBinding(root)
					if binding.SessionID != a.sessionID || !strings.Contains(strings.Join(binding.Args, " "), a.sessionID) {
						t.Fatal("resume lost the exact conversation")
					}
					if strings.Contains(strings.Join(binding.Env, " "), "stale-config") {
						t.Fatal("resume reused stale MCP environment")
					}
				}
				data, err := os.ReadFile(log)
				if err != nil {
					t.Fatal(err)
				}
				newWindow := false
				for _, line := range strings.Split(string(data), "\n") {
					newWindow = newWindow || strings.HasPrefix(line, "new-window ")
				}
				if !strings.Contains(string(data), "respawn-pane -k -t %1") || newWindow {
					t.Fatalf("recovery escaped the pinned pane: %s", data)
				}
			})
		}
	}
}
