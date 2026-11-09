package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/tmux"
)

// Board design rule: while the Board is on screen a
// permission request goes to the attention inbox and never opens a tmux
// popup. These tests drive the real permission queue against a fake tmux
// that records every command it is asked to run and would happily open a
// popup, so the only thing keeping one closed is the Board path itself.
// They run in both editions: Extreme applies reads automatically, so the
// request that reaches the inbox there is an owner-only kind.

// popupRecorder installs a fake tmux that logs each command's verb to a file
// and answers as an attached client in session "session" would. A popup it
// is asked to open is answered with key (A allow, D deny).
func popupRecorder(t *testing.T, w *Workspace, key string) func() []string {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	fake := filepath.Join(dir, "tmux")
	script := "#!/bin/bash\nprintf '%s\\n' \"$1\" >> '" + calls + "'\n" +
		"if [[ $* == *'#{client_height} #{client_width}'* ]]; then echo '40 120'; exit; fi\n" +
		"if [[ $1 == list-clients ]]; then printf 'client|%%marshal|session\\n'; exit; fi\n" +
		"if [[ $1 == display-message ]]; then printf '%%marshal\\n'; exit; fi\n" +
		"if [[ $1 == display-popup ]]; then printf '" + key + "' | bash -c \"${@: -1}\"; exit; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	t.Cleanup(tmux.ResetBinaryPath)
	w.tmuxPath, w.tmuxSession = fake, "session"
	w.tmuxMarshalPaneID = "%marshal"
	return func() []string {
		data, _ := os.ReadFile(calls)
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}
}

// popupOpened reports the popup-shaped commands among the recorded verbs:
// display-popup, or the new-window review tmux 3.2a falls back to.
func popupOpened(verbs []string) []string {
	var out []string
	for _, v := range verbs {
		if v == "display-popup" || v == "new-window" {
			out = append(out, v)
		}
	}
	return out
}

// boardPermissionRequests are the requests the test raises. Extreme applies
// reads on its own and leaves a worker's command proposal waiting for the
// Marshal; the project contract is owner-only in both editions.
func boardPermissionRequests(t *testing.T) (inbox []permission.Request, other []permission.Request) {
	contract := permission.Request{Kind: "project-contract", Object: "project contract revision 2", Scope: "this project", Who: "Marshal", Reason: "Adopt the reviewed contract"}
	read := permission.Request{Kind: "read", Object: t.TempDir(), Scope: "one run", Who: "Marshal", Reason: "Continue earlier work"}
	command := permission.Request{Kind: "marshal-command", Object: "/marshal settings control strict", Scope: "this project, next Marshal run", Who: "worker-1", Reason: "Apply the exact proposed setting"}
	if Edition() == "Marshal" {
		return []permission.Request{contract}, []permission.Request{read, command}
	}
	return []permission.Request{contract, read, command}, nil
}

func TestBoardPermissionRequestNeverOpensPopup(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-board-no-popup", false)
	verbs := popupRecorder(t, w, "A")
	w.screen = NewScreen(nil)
	w.mu.Lock()
	w.state.Marshal = &MarshalPanel{RunID: "run"}
	w.mu.Unlock()
	if !w.boardVisible() {
		t.Fatal("the Board is not on screen")
	}
	inboxReqs, otherReqs := boardPermissionRequests(t)
	for _, req := range append(append([]permission.Request(nil), inboxReqs...), otherReqs...) {
		w.permissions.queue.Add(req)
	}
	// Longer than one popup round trip: a popup, had one been chosen, would
	// have been issued and answered within this window.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	w.runPermissionQueue(ctx)

	if popups := popupOpened(verbs()); len(popups) != 0 {
		t.Fatalf("a popup was opened with the Board visible (%s); tmux calls: %v", Edition(), verbs())
	}
	got := map[string]bool{}
	for _, item := range w.boardInbox() {
		got[item.Request.Key()] = true
	}
	for _, req := range inboxReqs {
		if !got[req.Key()] {
			t.Fatalf("%s request %q did not reach the Board inbox (%s)", req.Kind, req.Object, Edition())
		}
	}
	events, err := runtime.Store().ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type != "PERMISSION_DECIDED" {
			continue
		}
		for _, req := range inboxReqs {
			if event.Data["object"] == req.Object {
				t.Fatalf("Board inbox request %q was decided without the operator: %v", req.Object, event.Data)
			}
		}
	}
}

// The control: the same fake tmux, the same owner-only request, the Board
// switched to the classic view. A popup is issued, so the recorder above
// would have seen one had the Board path let it through.
func TestBoardPermissionPopupRecorderSeesClassicPopup(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-board-popup-control", false)
	verbs := popupRecorder(t, w, "D")
	w.screen = NewScreen(nil)
	w.mu.Lock()
	w.state.Marshal = &MarshalPanel{RunID: "run"}
	w.boardClassic = true
	w.mu.Unlock()
	if w.boardVisible() {
		t.Fatal("classic view still reports the Board")
	}
	inboxReqs, _ := boardPermissionRequests(t)
	w.permissions.queue.Add(inboxReqs[0])
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	w.runPermissionQueue(ctx)
	if len(popupOpened(verbs())) == 0 {
		t.Fatalf("no popup without the Board (%s); the recorder cannot prove D20. tmux calls: %v", Edition(), verbs())
	}
}
