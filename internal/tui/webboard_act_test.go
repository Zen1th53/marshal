package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/chattransport"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/permission"
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
	// The card settles once its background reads (the architecture's
	// sketch) land: the frame is taken when two reads agree.
	f := webboard.FromBoard(w.webBoardFrame())
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		time.Sleep(150 * time.Millisecond)
		next := webboard.FromBoard(w.webBoardFrame())
		if webboard.CardID(next) == webboard.CardID(f) {
			break
		}
		f = next
	}
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
	res := w.webBoardAct(t.Context(), namedTestAction(w, webboard.Action{Kind: "key", Card: webboard.CardID(f), Key: "⏎"}))
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
	if res := w.webBoardAct(t.Context(), namedTestAction(w, webboard.Action{Kind: "key", Card: card, Key: "x"})); res.Status != "refused" || len(*ran) != 0 {
		t.Fatalf("unconfirmed stop: %+v ran %q", res, *ran)
	}
	res := w.webBoardAct(t.Context(), namedTestAction(w, webboard.Action{Kind: "key", Card: card, Key: "x", Confirmed: true}))
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
	res := w.webBoardAct(t.Context(), namedTestAction(w, webboard.Action{Kind: "key", Card: webboard.CardID(webCard(t, w)), Key: "d"}))
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
		{"stale card", namedTestAction(w, webboard.Action{Kind: "key", Card: "paused:000", Key: "⏎"}), "stale"},
		{"key not shown", namedTestAction(w, webboard.Action{Kind: "key", Card: card, Key: "r"}), "refused"},
		{"esc not on a paused card", namedTestAction(w, webboard.Action{Kind: "key", Card: card, Key: "esc"}), "refused"},
		{"typed text with nothing asking", namedTestAction(w, webboard.Action{Kind: "key", Card: card, Key: "⏎", Text: "R-9"}), "refused"},
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

// The page's action goes through the input loop when there is one; a
// command the key starts off the loop (the retry and the resume) is waited
// for, so "done" means it ran. An action the loop never reached is not run
// later.
func TestWebActRunsOnTheInputLoop(t *testing.T) {
	w, ran := webPausedWorkspace(t, leasePause)
	card := webboard.CardID(webCard(t, w))
	w.uiEvents = make(chan func(), 4)
	stop := make(chan struct{})
	loop := make(chan struct{})
	go func() {
		defer close(loop)
		for {
			select {
			case fn := <-w.uiEvents:
				fn()
			case <-stop:
				return
			}
		}
	}()
	res := w.webBoardAct(t.Context(), namedTestAction(w, webboard.Action{Kind: "key", Card: card, Key: "⏎"}))
	if res.Status != "done" || res.Text != "ok" || w.commandBusy.Load() {
		t.Fatalf("loop: %+v busy=%v", res, w.commandBusy.Load())
	}
	if want := []string{"/marshal retry T03 fresh-session", "/marshal resume"}; !slices.Equal(*ran, want) {
		t.Fatalf("ran %q, want %q", *ran, want)
	}
	close(stop)
	<-loop

	orig := webActWait
	t.Cleanup(func() { webActWait = orig })
	webActWait = 0
	*ran = nil
	res = w.webBoardAct(t.Context(), namedTestAction(w, webboard.Action{Kind: "key", Card: card, Key: "⏎"}))
	if res.Status != "refused" {
		t.Fatalf("busy loop: %+v", res)
	}
	for len(w.uiEvents) > 0 {
		(<-w.uiEvents)()
	}
	if len(*ran) != 0 {
		t.Fatalf("a timed-out action ran later: %q", *ran)
	}
}

// A command that fails is "refused" with the Board's own reason, not
// "done", and writes no "From the web" line for a thing that did not happen.
func TestWebActFailedCommandIsRefused(t *testing.T) {
	w, _ := webPausedWorkspace(t, leasePause)
	boardStopHandle = func(_ *Workspace, _ context.Context, cmd string) (string, error) {
		return "", errors.New("the run store is locked")
	}
	res := w.webBoardAct(t.Context(), namedTestAction(w, webboard.Action{Kind: "key", Card: webboard.CardID(webCard(t, w)), Key: "x", Confirmed: true}))
	if res.Status != "refused" || !strings.Contains(res.Text, "Could not stop the run") {
		t.Fatalf("result %+v", res)
	}
	if webNoteHas(w, "From the web") {
		t.Fatalf("activity %+v", w.noteList())
	}
}

// fakeChat is a native Marshal chat that records the turns it is sent.
type fakeChat struct {
	chattransport.Transport
	sent chan string
}

func (c *fakeChat) Send(_ context.Context, t chattransport.Turn) error {
	c.sent <- t.Text
	return nil
}

// A message from the web goes the way the Board's s composer sends it: to
// the Marshal chat as the owner's words, never run as a command, even when
// it starts with "/".
func TestWebSayReachesTheMarshalChat(t *testing.T) {
	w, ran := webPausedWorkspace(t, leasePause)
	c := &fakeChat{sent: make(chan string, 1)}
	w.chat.mu.Lock()
	w.chat.tr = c
	w.chat.mu.Unlock()
	t.Cleanup(func() { w.chat.mu.Lock(); w.chat.tr = nil; w.chat.mu.Unlock() })
	res := w.webBoardAct(t.Context(), webboard.Action{Kind: "say", Card: webboard.CardID(webCard(t, w)), Text: "/marshal stop"})
	if res.Status != "sent" {
		t.Fatalf("say: %+v", res)
	}
	select {
	case got := <-c.sent:
		if got != "/marshal stop" {
			t.Fatalf("the chat got %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the message never reached the chat")
	}
	if len(*ran) != 0 {
		t.Fatalf("the message ran as a command: %q", *ran)
	}
	if echo := w.sayEchoList(); len(echo) == 0 || echo[0].Text != "/marshal stop" || echo[0].Runtime {
		t.Fatalf("echo %+v", echo)
	}
	if !webNoteHas(w, "From the web: a message to the Marshal (sent)") {
		t.Fatalf("activity %+v", w.noteList())
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
	res := w.webBoardAct(t.Context(), namedTestAction(w, webboard.Action{Kind: "key", Card: webboard.CardID(f), Key: "a"}))
	if res.Status != "done" || res.Confirm == nil || !w.inboxView.Confirming() {
		t.Fatalf("a: %+v confirming=%v", res, w.inboxView.Confirming())
	}
	// Esc cancels the confirmation, as in the terminal.
	res = w.webBoardAct(t.Context(), namedTestAction(w, webboard.Action{Kind: "key", Card: webboard.CardID(webCard(t, w)), Key: "esc"}))
	if res.Status != "done" || w.inboxView.Confirming() {
		t.Fatalf("esc: %+v confirming=%v", res, w.inboxView.Confirming())
	}
}

// With no card on screen, "Stop all workers" is Ctrl+X: /stop all, run the
// way the key runs it, after the page's second click; never the Marshal.
func TestWebScreenKeysWithoutRuntime(t *testing.T) {
	w, _ := webPausedWorkspace(t, leasePause)
	w.webBoard()
	state, _ := webBoards.Load(w)
	state.(*webBoardState).retry = "T03"
	if keys := w.webScreenKeys(); len(keys) != 0 {
		t.Fatalf("unsupported controls: %+v", keys)
	}
}

// namedTestAction builds the typed request for the button a terminal key names.
func namedTestAction(w *Workspace, a webboard.Action) webboard.Action {
	f := webboard.FromBoard(w.webBoardFrame())
	a.Kind, a.Revision = "decide", webboard.CardID(f)
	if f.Paused != nil {
		a.Choice = webboard.PauseOp(a.Key)
	} else if a.Key == "esc" {
		a.Choice = "card/dismiss"
	} else if f.Decision != nil {
		for _, c := range f.Decision.Choices {
			if c.Key == a.Key {
				a.Choice, a.Value = c.ID, c.Value
				a.Revision = f.Decision.Revision
				if f.Decision.Plan != nil {
					a.Digest = f.Decision.Plan.Digest
				}
				break
			}
		}
	}
	return a
}
