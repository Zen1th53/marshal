//go:build linux

package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// The quota card's main button from the web runs the reassigns and the
// resume that Enter runs (TestPausedCardQuotaWithTwoTasks).
func TestWebActQuotaMoveIsTheKey(t *testing.T) {
	ran := recordStopCommands(t)
	w, _ := quotaWorkspace(t, 52)
	f := webCard(t, w)
	res := w.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: webboard.CardID(f), Key: "⏎"})
	if res.Status != "done" {
		t.Fatalf("result %+v", res)
	}
	if want := []string{"/marshal reassign T06 claude", "/marshal reassign T09 claude", "/marshal resume"}; !slices.Equal(*ran, want) {
		t.Fatalf("web ran %q, want %q", *ran, want)
	}
}

// "Open in browser" on the architecture card is the terminal's a; the
// review's private link comes back to the page only, never into Activity,
// and nothing is accepted.
func TestWebActOpensTheArchitectureReview(t *testing.T) {
	displaySeams(t, "darwin", nil)
	w := architectureWorkspace(t)
	f := webCard(t, w)
	if f.Decision == nil || f.Decision.Kind != decisionArchitecture {
		t.Fatalf("card %+v", f.Decision)
	}
	res := w.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: webboard.CardID(f), Key: "a"})
	if res.Status != "done" || res.Link != "http://127.0.0.1:37667/?token=7964abcd462b" {
		t.Fatalf("result %+v", res)
	}
	for _, n := range w.noteList() {
		if strings.Contains(n.Text, "token=") {
			t.Fatalf("the review link reached Activity: %q", n.Text)
		}
	}
	if !webNoteHas(w, "From the web: open in browser") {
		t.Fatalf("activity %+v", w.noteList())
	}
	// Enter is not on the card: the page cannot accept for the owner.
	if r := w.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: webboard.CardID(webCard(t, w)), Key: "⏎"}); r.Status != "refused" {
		t.Fatalf("Enter on the architecture card: %+v", r)
	}
}

// The inbox's allow is two presses, as in the terminal: a opens the
// confirmation (the page gets a Confirm step), Enter decides.
func TestWebActInboxAllowIsTwoSteps(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	defer v2Cards.Delete(w)
	w.permissions.mu.Lock()
	w.permissions.inbox = []permission.Request{{Kind: "network", Object: "proxy.golang.org", Reason: "go mod download", RunID: "R-1", TaskID: "T03"}}
	w.permissions.mu.Unlock()
	f := webCard(t, w)
	res := w.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: webboard.CardID(f), Key: "a"})
	if res.Status != "done" || res.Confirm == nil || !w.inboxView.Confirming() {
		t.Fatalf("a: %+v confirming=%v", res, w.inboxView.Confirming())
	}
	// Esc cancels the confirmation, as in the terminal.
	res = w.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: webboard.CardID(webCard(t, w)), Key: "esc"})
	if res.Status != "done" || w.inboxView.Confirming() {
		t.Fatalf("esc: %+v confirming=%v", res, w.inboxView.Confirming())
	}
}
