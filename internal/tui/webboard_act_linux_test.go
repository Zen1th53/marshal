//go:build linux

package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
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
	// The live review session holds the link (no file does); the card the
	// page saw already knows it, so the action is not stale.
	withArchitectureLink(w)
	f := webCard(t, w)
	if f.Decision == nil || f.Decision.Kind != decisionArchitecture {
		t.Fatalf("card %+v", f.Decision)
	}
	res := w.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: webboard.CardID(f), Key: "a"})
	// The open may fail in a test workspace (no review server); then the
	// page is told so, and no link is sent.
	switch {
	case res.Status == "done" && res.Link == fixtureArchitectureLink:
		if !webNoteHas(w, "From the web: open in browser") {
			t.Fatalf("activity %+v", w.noteList())
		}
	case res.Status == "refused" && strings.Contains(res.Text, "could not be opened") && res.Link == "":
	default:
		t.Fatalf("result %+v", res)
	}
	for _, n := range w.noteList() {
		if strings.Contains(n.Text, "token=") {
			t.Fatalf("the review link reached Activity: %q", n.Text)
		}
	}
	// Enter is not on the card: the page cannot accept for the owner.
	if r := w.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: webboard.CardID(webCard(t, w)), Key: "⏎"}); r.Status != "refused" {
		t.Fatalf("Enter on the architecture card: %+v", r)
	}
}

// With no card on screen, "Retry <task>" is offered only while r would
// retry a stuck task, and a click (confirmed) is r then Enter: the retry
// in a fresh session through boardRetry, as the keys run it.
func TestWebActRetryIsTheKey(t *testing.T) {
	orig := boardRetry
	t.Cleanup(func() { boardRetry = orig })
	var calls []string
	boardRetry = func(_ *Workspace, _ context.Context, id string) (string, error) {
		calls = append(calls, id)
		return id + " starts again in a fresh session.", nil
	}
	w, _ := boardWorkspace(t)
	defer w.Close()
	defer v2Cards.Delete(w)
	w.webBoard() // a Web Board is open
	w.permissions.inbox = nil
	w.noteWebRetry()
	if f := webboard.FromBoard(w.webBoardFrame()); slices.ContainsFunc(f.Keys, func(k webboard.Key) bool { return k.Key == "r" }) {
		t.Fatalf("Retry offered with no task under the keyboard: %+v", f.Keys)
	}
	w.boardUI.focus, w.boardUI.task, w.boardUI.inspect = layout.Tasks, "T-023", ""
	w.noteWebRetry()
	f := webboard.FromBoard(w.webBoardFrame())
	if f.Decision != nil || f.Paused != nil {
		t.Fatalf("a card is shown: %+v %+v", f.Decision, f.Paused)
	}
	if !slices.Contains(webboard.ActionKeys(f), "r") || !slices.Contains(webboard.ActionKeys(f), "ctrl+x") {
		t.Fatalf("screen keys %+v", f.Keys)
	}
	card := webboard.CardID(f)
	if res := w.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: card, Key: "r"}); res.Status != "refused" || len(calls) != 0 {
		t.Fatalf("unconfirmed retry: %+v calls %q", res, calls)
	}
	res := w.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: card, Key: "r", Confirmed: true})
	w.commandWG.Wait()
	if res.Status != "done" || !slices.Equal(calls, []string{"T-023"}) || w.boardUI.retryArmed != "" {
		t.Fatalf("retry: %+v calls %q armed %q", res, calls, w.boardUI.retryArmed)
	}
	if !webNoteHas(w, "From the web: retry T-023") {
		t.Fatalf("activity %+v", w.noteList())
	}
}
