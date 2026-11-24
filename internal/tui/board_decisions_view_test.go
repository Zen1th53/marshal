package tui

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/usage"
)

func dvKey(w *Workspace, r rune) bool { return w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: r}) }

// dvStopRecorder replaces the stop commands' handler with one that records
// what ran.
func dvStopRecorder(t *testing.T) *[]string {
	t.Helper()
	orig := boardStopHandle
	t.Cleanup(func() { boardStopHandle = orig })
	var ran []string
	boardStopHandle = func(_ *Workspace, _ context.Context, cmd string) (string, error) {
		ran = append(ran, cmd)
		return "ok", nil
	}
	return &ran
}

// dvPausedWorkspace is a Board whose run is paused on two tasks by a
// provider limit, with room on another provider.
func dvPausedWorkspace(t *testing.T) (*Workspace, time.Time) {
	t.Helper()
	w := emptyBoardWorkspace(t)
	t.Cleanup(w.Close)
	t.Cleanup(func() { v2Cards.Delete(w) })
	reset := time.Now().Add(65 * time.Minute)
	run := marshal.Run{
		State:    marshal.AwaitingUser,
		PlanID:   "PLAN-1",
		Settings: marshal.Settings{ReworkLimit: 2},
		Pause:    &marshal.Pause{Reason: "provider limit: agy reached its usage limit", Detail: "agy: RESOURCE_EXHAUSTED 429 quota", Code: marshal.CodeProviderLimit, Action: marshal.PauseResume, Until: reset},
		Tasks: []marshal.Task{
			{PlanTaskID: "T06", Title: "history paging", Worker: "agy", State: marshal.Escalated, Branch: "marshal/T06", BaseCommit: "base", FailedAttemptCommit: "tip6"},
			{PlanTaskID: "T09", Title: "dashed-change styles", Worker: "agy", State: marshal.Escalated, Branch: "marshal/T09", BaseCommit: "base", FailedAttemptCommit: "tip9"},
			{PlanTaskID: "T01", Title: "store", Worker: "claude", State: marshal.Merged},
		},
	}
	w.setMarshalPanel(newMarshalPanel("R-9", "claude", run, "paused"))
	w.usage.mu.Lock()
	w.usage.quotas = []usage.Quota{
		{Provider: "agy", Known: true, Windows: []usage.Window{{Label: "Gemini 5h", LeftPercent: 0, ResetsAt: reset}}},
		{Provider: "claude", Known: true, Windows: []usage.Window{{Label: "5h", LeftPercent: 52, ResetsAt: time.Now().Add(3 * time.Hour)}}},
	}
	w.usage.read = true
	w.usage.mu.Unlock()
	orig := v2CommitCount
	t.Cleanup(func() { v2CommitCount = orig })
	v2CommitCount = func(_ context.Context, _, _, tip string) (int, error) {
		return map[string]int{"tip6": 3, "tip9": 1}[tip], nil
	}
	w.quotaCard.waitFn = func(ctx context.Context, _ time.Time) bool { <-ctx.Done(); return false }
	w.quotaCard.freeFn = func() bool { return false }
	w.quotaCard.resumeFn = func(context.Context) {}
	return w, reset
}

// dvWaitPaused waits for the commit counts the card reads in the background.
func dvWaitPaused(t *testing.T, w *Workspace) v2Pause {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		z, ok := w.v2PauseNow(time.Now())
		if ok && len(z.card.Tasks) == 2 && strings.Contains(z.card.Tasks[1].Kept, "commit") {
			return z
		}
		if time.Now().After(deadline) {
			t.Fatalf("paused card never filled in: %+v", z.card)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func dvInbox(w *Workspace, objects ...string) []permission.Request {
	var reqs []permission.Request
	for _, o := range objects {
		reqs = append(reqs, permission.Request{Kind: "network", Object: o, Reason: "go mod download", RunID: "R-1", TaskID: "T03"})
	}
	w.permissions.mu.Lock()
	w.permissions.inbox = reqs
	w.permissions.mu.Unlock()
	return reqs
}

// A decision key acts on the request the card showed, not on whichever
// request the inbox happens to select, and a card that changed closes
// the focus instead of acting on the new one.
func TestTerminalInboxDecisionUsesCurrentBinding(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	defer v2Cards.Delete(w)
	reqs := dvInbox(w, "proxy.golang.org", "example.com")
	items := w.boardInbox()
	if len(items) != 2 {
		t.Fatalf("inbox has %d items", len(items))
	}
	d, ok := w.v2DecisionNow(time.Now())
	if !ok || d.kind != decisionInbox || d.key != "inbox:"+items[0].Canonical {
		t.Fatalf("card %+v", d)
	}
	// The inbox selects the second request while the card names the first.
	w.inboxView.Update(items)
	w.inboxView.Select(items[1].Request.Key())

	if !dvKey(w, 'o') || w.boardUI.decisionFocus != focusDecision {
		t.Fatal("o did not focus the decision")
	}
	if !dvKey(w, 'a') || !w.inboxView.Confirming() {
		t.Fatal("a did not begin the allow")
	}
	dec, ok := w.inboxView.Confirm()
	if !ok || dec.RequestKey != items[0].Request.Key() || dec.Canonical != items[0].Canonical || !dec.Allow {
		t.Fatalf("decision %+v, want the card's request %s", dec, items[0].Canonical)
	}
	w.inboxView.Cancel()

	// A new request ahead of it changes the card: the old binding is gone.
	w.permissions.mu.Lock()
	w.permissions.inbox = append([]permission.Request{{Kind: "network", Object: "evil.example", Reason: "x", RunID: "R-1", TaskID: "T04", Who: "agent"}}, reqs...)
	w.permissions.mu.Unlock()
	if next := w.boardInbox(); next[0].Canonical == items[0].Canonical {
		t.Skip("the new request does not sort first in this edition")
	}
	if !dvKey(w, 'a') || w.inboxView.Confirming() || w.boardUI.decisionFocus != "" {
		t.Fatalf("a on a changed card: confirming=%v focus=%q", w.inboxView.Confirming(), w.boardUI.decisionFocus)
	}
	if !strings.Contains(dvOutput(w), "changed") {
		t.Fatalf("answer %q", dvOutput(w))
	}
}

// Every key the paused card draws is reachable from decision focus.
func TestPausedCardAllActionsReachable(t *testing.T) {
	ran := dvStopRecorder(t)
	w, reset := dvPausedWorkspace(t)
	z := dvWaitPaused(t, w)
	var keys []string
	for _, k := range z.card.Actions {
		keys = append(keys, k.Key)
	}
	if want := []string{"⏎", "w", "d", "x"}; !slices.Equal(keys, want) {
		t.Fatalf("actions %q, want %q (reset %s)", keys, want, reset)
	}
	open := func() {
		t.Helper()
		if !dvKey(w, 'o') || w.boardUI.decisionFocus != focusPaused {
			t.Fatalf("o did not focus the paused card: %q", w.boardUI.decisionFocus)
		}
	}
	open()
	if !dvKey(w, 'd') || !strings.HasPrefix(dvOutput(w), "Raw error: agy: RESOURCE_EXHAUSTED") {
		t.Fatalf("d: %q", dvOutput(w))
	}
	if !dvKey(w, 'w') || !w.quotaCard.isArmed() {
		t.Fatal("w did not wait for the reset")
	}
	w.disarmQuotaResume()
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) || !slices.Equal(*ran, []string{"/marshal reassign T06 claude", "/marshal reassign T09 claude", "/marshal resume"}) {
		t.Fatalf("Enter ran %q", *ran)
	}
	*ran = nil
	// x asks again; a second x stops the run and keeps its work.
	if !dvKey(w, 'x') || len(*ran) != 0 || !w.boardUI.decisionStop {
		t.Fatalf("x ran %q before it asked again", *ran)
	}
	if !dvKey(w, 'x') || !slices.Equal(*ran, []string{"/marshal stop"}) || w.boardUI.decisionFocus != "" {
		t.Fatalf("second x ran %q focus %q", *ran, w.boardUI.decisionFocus)
	}
}

// The pane says what Enter does, when the limit resets, the affected tasks
// and what is kept; keys go to the editor first, then an armed stop, then
// the card, and the pane closes when the card changes.
func TestPausedExplanationAndKeyFocusMatrix(t *testing.T) {
	ran := dvStopRecorder(t)
	w, reset := dvPausedWorkspace(t)
	z := dvWaitPaused(t, w)
	b := w.boardV2()
	if b.Paused == nil {
		t.Fatal("no paused card on the Board")
	}
	text := strings.Join(pausedPaneLines(*b.Paused, pausedRevision(z), false), "\n")
	for _, want := range []string{
		"PAUSED · agy Gemini 5h limit",
		"Resets at " + resetClock(reset, time.Now()),
		"T06 history paging", "branch + 3 commits", "T09 dashed-change styles", "branch + 1 commit",
		"ENTER DOES", "move T06 and T09 to claude in fresh sessions, keep commits, and resume the run.",
		"w  wait until", "d  raw error", "x  stop the run", "Bound to paused:quota:T06,T09",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("pane lacks %q:\n%s", want, text)
		}
	}

	// Keys that open nothing here change nothing: no focus, no stop.
	if dvKey(w, 'z') || w.boardUI.decisionFocus != "" {
		t.Fatal("a key with no card focus was taken")
	}
	// The editor wins over the card.
	dvKey(w, 'o')
	w.openSay()
	dvKey(w, 'x')
	if w.boardUI.decisionStop || len(*ran) != 0 {
		t.Fatal("x reached the card while the editor was open")
	}
	w.say.open.Store(false)
	// A view over the Board wins over the card.
	w.hostView.Store(true)
	dvKey(w, 'x')
	w.hostView.Store(false)
	if w.boardUI.decisionStop {
		t.Fatal("x reached the card while a view was open")
	}
	// An armed stop is cancelled by any other key, before anything else.
	dvKey(w, 'x')
	if !w.boardUI.decisionStop {
		t.Fatal("x did not arm the stop")
	}
	dvKey(w, 'w')
	if w.boardUI.decisionStop || w.quotaCard.isArmed() || len(*ran) != 0 || w.boardUI.decisionFocus != focusPaused {
		t.Fatalf("a key after x: stop armed=%v wait armed=%v ran %q focus %q", w.boardUI.decisionStop, w.quotaCard.isArmed(), *ran, w.boardUI.decisionFocus)
	}
	// Esc on an armed stop disarms it and keeps focus.
	dvKey(w, 'x')
	w.handleBoardKey(KeyEvent{Type: KeyEsc})
	if w.boardUI.decisionStop || w.boardUI.decisionFocus != focusPaused {
		t.Fatal("Esc on an armed stop did not disarm it and keep focus")
	}
	// The painted pane keeps its choices, Enter line and binding on a
	// small screen.
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, false), now: time.Now()}
	for _, size := range [][2]int{{80, 24}, {60, 20}} {
		frame := strings.Join(w.paintDecisionPane(v, w.boardV2(), size[0], size[1]), "\n")
		for _, want := range []string{"ENTER DOES", "x stop the run", "Bound to paused:quota"} {
			if !strings.Contains(frame, want) {
				t.Errorf("%dx%d frame lacks %q:\n%s", size[0], size[1], want, frame)
			}
		}
	}
	// Esc leaves focus; a key the card does not document goes on.
	if dvKey(w, 'z') {
		t.Fatal("z was taken by the card")
	}
	w.handleBoardKey(KeyEvent{Type: KeyEsc})
	if w.boardUI.decisionFocus != "" {
		t.Fatal("Esc did not leave focus")
	}
	// The card changes under the pane: the key does nothing and the pane
	// closes.
	dvKey(w, 'o')
	w.mu.Lock()
	w.state.Marshal.run.Tasks[1].State = marshal.Merged
	w.mu.Unlock()
	dvKey(w, 'x')
	if w.boardUI.decisionFocus != "" || w.boardUI.decisionStop || !strings.Contains(dvOutput(w), "changed") {
		t.Fatalf("changed card: focus %q stop %v answer %q", w.boardUI.decisionFocus, w.boardUI.decisionStop, dvOutput(w))
	}
}

func dvOutput(w *Workspace) string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.state.LastOutput
}
