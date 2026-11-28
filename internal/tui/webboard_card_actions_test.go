package tui

import (
	"reflect"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/webboard"
)

func TestNamedPauseActionsMatchTerminal(t *testing.T) {
	for _, tc := range []struct {
		op    string
		event KeyEvent
	}{
		{"pause/continue", KeyEvent{Type: KeyEnter}},
		{"pause/wait", KeyEvent{Type: KeyRune, Rune: 'w'}},
		{"pause/details", KeyEvent{Type: KeyRune, Rune: 'd'}},
		{"pause/stop", KeyEvent{Type: KeyRune, Rune: 'x'}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			pause := leasePause
			if tc.op == "pause/wait" {
				pause.Until = time.Now().Add(time.Hour).Truncate(time.Minute)
			}
			w, calls := webPausedWorkspace(t, pause)
			f := webCard(t, w)

			id := webboard.CardID(f)
			a := webboard.Action{Kind: "decide", Card: id, Revision: id, Choice: tc.op, Confirmed: true}
			stale := a
			stale.Revision = "old"
			if res, _, _ := w.webCardAction(f, stale); res.Status != "stale" || len(*calls) != 0 {
				t.Fatalf("stale: %+v %v", res, *calls)
			}
			res, started, _ := w.webCardAction(f, a)
			if started {
				t.Fatal("unexpected asynchronous action")
			}
			wantCalls := append([]string(nil), (*calls)...)
			terminal, terminalCalls := webPausedWorkspace(t, pause)
			before := terminal.outputSeq.Load()
			if !terminal.handleV2PauseKey(tc.event, mustPause(t, terminal)) {
				t.Fatal("terminal refused action")
			}
			terminal.mu.RLock()
			want := webboard.Result{Status: "done", Text: terminal.answer}
			if terminal.answerIsError && tc.op != "pause/details" {
				want.Status = "refused"
				if terminal.lastRefusal != nil {
					want = want.WithRefusal(terminal.lastRefusal.Clone())
				}
			}
			terminal.mu.RUnlock()
			if terminal.outputSeq.Load() == before {
				want.Text = ""
			}
			if !reflect.DeepEqual(res, want) || !reflect.DeepEqual(wantCalls, *terminalCalls) {
				t.Fatalf("web %+v %v; terminal %+v %v", res, wantCalls, want, *terminalCalls)
			}
		})
	}
}

func mustPause(t *testing.T, w *Workspace) v2Pause {
	t.Helper()
	z, ok := w.v2PauseNow(boardNow())
	if !ok {
		t.Fatal("no paused card")
	}
	return z
}

func TestNamedDismissKeepsPendingDecision(t *testing.T) {
	w, _ := webPausedWorkspace(t, leasePause)
	w.guard.alert = &guardAlert{at: time.Now(), paths: []string{"file.go"}, wasDirty: map[string]bool{}}
	f := webCard(t, w)
	id := webboard.CardID(f)
	a := webboard.Action{Kind: "decide", Card: id, Revision: "old", Choice: "card/dismiss"}
	if res := w.webBoardAct(t.Context(), a); res.Status != "stale" {
		t.Fatalf("stale: %+v", res)
	}
	a.Revision = id
	if res := w.webBoardAct(t.Context(), a); res.Status != "done" {
		t.Fatalf("dismiss: %+v", res)
	}
	if w.guard.alert == nil {
		t.Fatal("dismiss resolved the pending decision")
	}
	if d, ok := w.v2DecisionNow(boardNow()); ok && d.kind == decisionGuard {
		t.Fatal("dismissed decision still shown")
	}
}
