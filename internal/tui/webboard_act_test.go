package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// webPausedWorkspace is a run paused on one task by pause; the stop
// commands its keys run are recorded instead of run.
func webPausedWorkspace(t *testing.T, pause marshal.Pause) (*Workspace, *[]string) {
	t.Helper()
	orig := boardStopHandle
	t.Cleanup(func() { boardStopHandle = orig })
	var ran []string
	boardStopHandle = func(_ *Workspace, _ context.Context, cmd string) (string, error) {
		ran = append(ran, cmd)
		return "ok", nil
	}
	w := NewWorkspace(nil, "project", "session")
	w.screen = NewScreen(nil)
	t.Cleanup(w.Close)
	t.Cleanup(func() { v2Cards.Delete(w) })
	run := marshal.Run{
		State: marshal.AwaitingUser, PlanID: "PLAN-1", Settings: marshal.Settings{ReworkLimit: 2}, Pause: &pause,
		Tasks: []marshal.Task{{PlanTaskID: "T03", Title: "sub-view links", Worker: "codex", State: marshal.Escalated, Branch: "marshal/T03"}},
	}
	w.setMarshalPanel(newMarshalPanel("R-9", "codex", run, "paused"))
	return w, &ran
}

var leasePause = marshal.Pause{Reason: "codex failed 4 times in a row", Detail: "infra retry: task time ran out", Code: marshal.CodeInfraRetry, Action: marshal.PauseRetryTask, TaskID: "T03"}

// webCard is the card id and frame the page would see now.
func webCard(t *testing.T, w *Workspace) webboard.Frame {
	t.Helper()
	f := webboard.FromBoard(w.webBoardFrame())
	if f.Paused == nil && f.Decision == nil {
		t.Fatalf("no card on screen: %q", f.Screen)
	}
	return f
}

func webNoteHas(w *Workspace, text string) bool {
	for _, n := range w.noteList() {
		if strings.Contains(n.Text, text) {
			return true
		}
	}
	return false
}

// The paused card's main button from the web runs exactly what Enter runs
// in the terminal: the retry in a fresh session, then the resume.
func TestWebActPausedEnterIsTheKey(t *testing.T) {
	w, ran := webPausedWorkspace(t, leasePause)
	f := webCard(t, w)
	if f.Paused.Actions[0].Key != "⏎" || !strings.HasPrefix(f.Paused.Actions[0].Label, "retry T03") {
		t.Fatalf("actions %+v", f.Paused.Actions)
	}
	res := w.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: webboard.CardID(f), Key: "⏎"})
	if res.Status != "done" {
		t.Fatalf("result %+v", res)
	}
	want := []string{"/marshal retry T03 fresh-session", "/marshal resume"}
	if !slices.Equal(*ran, want) {
		t.Fatalf("web ran %q, want %q", *ran, want)
	}

	// The terminal key on a fresh, identical workspace runs the same.
	w2, ran2 := webPausedWorkspace(t, leasePause)
	if !w2.handleV2CardKey(KeyEvent{Type: KeyEnter}) || !slices.Equal(*ran2, *ran) {
		t.Fatalf("key ran %q, web ran %q", *ran2, *ran)
	}
	if !webNoteHas(w, "From the web: retry T03") {
		t.Fatalf("no web Activity line: %+v", w.noteList())
	}
}

// Stop from the web needs the page's second click; then it is the
// terminal's x (/marshal stop), with its own Activity line.
func TestWebActStopNeedsConfirmation(t *testing.T) {
	w, ran := webPausedWorkspace(t, leasePause)
	card := webboard.CardID(webCard(t, w))
	if res := w.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: card, Key: "x"}); res.Status != "refused" || len(*ran) != 0 {
		t.Fatalf("unconfirmed stop: %+v ran %q", res, *ran)
	}
	res := w.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: card, Key: "x", Confirmed: true})
	if res.Status != "done" || !slices.Equal(*ran, []string{"/marshal stop"}) || !strings.HasPrefix(res.Text, "Stopped.") {
		t.Fatalf("stop: %+v ran %q", res, *ran)
	}
	if !webNoteHas(w, "Stopped here.") || !webNoteHas(w, "From the web: stop the run") {
		t.Fatalf("activity %+v", w.noteList())
	}
}

// d answers with the raw error, the text the terminal shows.
func TestWebActRawError(t *testing.T) {
	w, _ := webPausedWorkspace(t, leasePause)
	res := w.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: webboard.CardID(webCard(t, w)), Key: "d"})
	if res.Status != "done" || res.Text != "Raw error: infra retry: task time ran out" {
		t.Fatalf("d: %+v", res)
	}
}

// An action for another card, a key the card does not show, or quit is
// refused and runs nothing.
func TestWebActRefusals(t *testing.T) {
	w, ran := webPausedWorkspace(t, leasePause)
	card := webboard.CardID(webCard(t, w))
	for _, c := range []struct {
		name string
		a    webboard.Action
		want string
	}{
		{"stale card", webboard.Action{Kind: "key", Card: "paused:000", Key: "⏎"}, "stale"},
		{"key not shown", webboard.Action{Kind: "key", Card: card, Key: "r"}, "refused"},
		{"esc not on a paused card", webboard.Action{Kind: "key", Card: card, Key: "esc"}, "refused"},
		{"typed text with nothing asking", webboard.Action{Kind: "key", Card: card, Key: "⏎", Text: "R-9"}, "refused"},
		{"unknown kind", webboard.Action{Kind: "command", Card: card, Text: "/marshal stop"}, "refused"},
	} {
		if res := w.webBoardAct(t.Context(), c.a); res.Status != c.want {
			t.Errorf("%s: %+v", c.name, res)
		}
	}
	if len(*ran) != 0 {
		t.Fatalf("a refused action ran %q", *ran)
	}
}

// With no Marshal chat open a message is refused and nothing goes into the
// terminal's composer.
func TestWebSayWithoutChat(t *testing.T) {
	w, _ := webPausedWorkspace(t, leasePause)
	res := w.webBoardAct(t.Context(), webboard.Action{Kind: "say", Card: webboard.CardID(webCard(t, w)), Text: "/marshal stop"})
	if res.Status != "refused" || !strings.Contains(res.Text, "No Marshal chat is open") {
		t.Fatalf("say: %+v", res)
	}
	if w.composing() || w.composer.Text() != "" {
		t.Fatal("the web message landed in the terminal's composer")
	}
}

// The page's action goes through the input loop when there is one, and an
// action the loop never reached is not run later.
func TestWebActRunsOnTheInputLoop(t *testing.T) {
	w, ran := webPausedWorkspace(t, leasePause)
	card := webboard.CardID(webCard(t, w))
	w.uiEvents = make(chan func(), 1)
	done := make(chan webboard.Result, 1)
	go func() { done <- w.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: card, Key: "⏎"}) }()
	fn := <-w.uiEvents
	fn()
	if res := <-done; res.Status != "done" || len(*ran) != 2 {
		t.Fatalf("loop: %+v ran %q", res, *ran)
	}

	orig := webActWait
	t.Cleanup(func() { webActWait = orig })
	webActWait = 0
	*ran = nil
	res := w.webBoardAct(t.Context(), webboard.Action{Kind: "key", Card: card, Key: "⏎"})
	if res.Status != "refused" {
		t.Fatalf("busy loop: %+v", res)
	}
	select {
	case fn := <-w.uiEvents:
		fn()
	default:
	}
	if len(*ran) != 0 {
		t.Fatalf("a timed-out action ran later: %q", *ran)
	}
}
