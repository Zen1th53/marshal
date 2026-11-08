//go:build governed

package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestMarshalProposalNavigatesChatToCentreOnce(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-surfaces", false)
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	t.Setenv("POPUP_FIXTURE", dir)
	script := `#!/bin/bash
if [[ $* == *'#{client_height} #{client_width}'* ]]; then echo '40 120'; exit; fi
case "$1" in
 list-clients)
  if [[ -f "$POPUP_FIXTURE/centre" ]]; then printf 'client|%%centre|session\n'; else printf 'client|%%chat|session\n'; fi;;
 display-message) echo 3.3a;;
 switch-client) printf '%s\n' "$*" > "$POPUP_FIXTURE/navigation"; touch "$POPUP_FIXTURE/centre";;
 display-popup)
  printf popup\\n >> "$POPUP_FIXTURE/calls"
  if [[ -f "$POPUP_FIXTURE/centre" ]]; then printf A; else printf '\033[23~'; fi | bash -c "${@: -1}";;
 *) exit 1;;
esac
`
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	w.tmuxSession = "session"
	w.tmuxPath = fake
	w.tmuxMarshalPaneID = "%centre"
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": {role: "marshal-chat", paneID: "%chat"}}
	w.observeMarshalProposals(importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"setting","key":"control","value":"strict"}`}}})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	w.runPermissionQueue(ctx)
	calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
	if strings.Count(string(calls), "popup") != 2 {
		t.Fatalf("expected popup on chat and centre: %q", calls)
	}
	navigation, _ := os.ReadFile(filepath.Join(dir, "navigation"))
	if !strings.Contains(string(navigation), "switch-client -c client -t %centre") {
		t.Fatal(string(navigation))
	}
	settings, _ := rt.Marshal().Store.GetMarshalSettings(t.Context(), rt.ProjectID())
	if string(settings.Value.Control) != "strict" {
		t.Fatal("navigation silently denied")
	}
	events, _ := rt.Store().ListEvents(t.Context())
	count := 0
	for _, event := range events {
		if event.Type == "PERMISSION_DECIDED" {
			count++
			if event.Data["allow"] != true {
				t.Fatal("navigation recorded denial")
			}
		}
	}
	if count != 1 {
		t.Fatalf("decisions=%d", count)
	}
	w.mu.RLock()
	visible := strings.Contains(w.state.LastOutput, "/marshal settings control strict")
	w.mu.RUnlock()
	if !visible {
		t.Fatal("centre has no pending request display")
	}
}
