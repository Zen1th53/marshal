//go:build linux

package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tmux"
)

func assertNativePaneInput(t *testing.T, ctx context.Context, pane, want string) {
	t.Helper()
	out, err := tmux.RunCommand(ctx, "display-message", "-p", "-t", pane, "#{pane_input_off}")
	if err != nil || strings.TrimSpace(string(out)) != want {
		t.Fatalf("pane_input_off = %q, want %s: %v", out, want, err)
	}
}

func TestNativeOperatorProviderPaneInputEnabled(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "opencode", "antigravity"} {
		t.Run(provider, func(t *testing.T) {
			w := realTmuxWorkspace(t)
			ctx := t.Context()
			msg, err := w.runNativeAgentInTmux(ctx, nativeLaunchOperator, provider, provider, w.workDir, "/bin/sh", []string{"-c", "echo READY; while read answer; do echo received:$answer; done"}, nil, nil, nil, nil, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(msg, "view-only") || strings.Contains(msg, "/takeover") {
				t.Fatalf("operator launch message: %s", msg)
			}
			w.tmuxMu.Lock()
			a := copyAgentLocked(w.tmuxActiveWins[provider])
			w.tmuxMu.Unlock()
			if a == nil || a.readOnly || a.launchOrigin != nativeLaunchOperator {
				t.Fatalf("operator launch state: %+v", a)
			}
			assertNativePaneInput(t, ctx, a.paneID, "0")
			waitForTmuxOutput(t, a.paneID, "READY")
			if _, err := tmux.RunCommand(ctx, "send-keys", "-t", a.paneID, "operator-input", "Enter"); err != nil {
				t.Fatal(err)
			}
			waitForTmuxOutput(t, a.paneID, "received:operator-input")
			// Switching back to this surviving session must preserve input readiness.
			if _, err := w.runNativeAgentInTmux(ctx, nativeLaunchOperator, provider, provider, w.workDir, "/bin/sh", nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			assertNativePaneInput(t, ctx, a.paneID, "0")
		})
	}
}
