package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/permission"
)

func resetOwnPastes() {
	ownPastes.mu.Lock()
	ownPastes.panes = map[string]ownPaste{}
	ownPastes.mu.Unlock()
}

func claudeFakeWorkspace(t *testing.T, drawn string) (*Workspace, *fakeClaudePane) {
	t.Helper()
	resetOwnPastes()
	w := emptyBoardWorkspace(t)
	t.Cleanup(w.Close)
	f := &fakeClaudePane{
		suggestion: readFrame(t, "mac_frame_suggestion.txt"),
		drawnFrame: readFrame(t, drawn),
		busy:       true,
	}
	w.chatRun = f.run
	w.tmuxMu.Lock()
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", paneID: "%7", window: "marshal-chat-fake", provider: "claude"}
	w.tmuxMu.Unlock()
	return w, f
}

// An answer pasted but not yet taken says so; once the chat takes it the
// footer says it was sent, and the ask card's receipt is admitted.
func TestAskAnswerNotSentStatusIsReplacedWhenTaken(t *testing.T) {
	const label = "Start from main, land on main"
	w, f := claudeFakeWorkspace(t, "mac_frame.txt")
	choices := `[{"key":"enter","label":"` + label + `"},{"key":"f","label":"Land on a new feature branch"}]`
	p, err := parseMarshalProposal(ownPasteAskLine("Which branch should the work start from and land on?", choices))
	if err != nil {
		t.Fatal(err)
	}
	w.openAsk(p)
	card := w.currentAsk()
	if card == nil {
		t.Fatal("the card did not open")
	}
	id := card.ID
	w.answerAsk(0)
	if out := stOutput(w); !strings.Contains(out, "The answer was not sent") {
		t.Fatalf("status %q", out)
	}
	f.mu.Lock()
	f.busy = false
	f.mu.Unlock()
	w.submitOwnLeftover(context.Background(), "%7", false)
	if out := stOutput(w); strings.Contains(out, "not sent") || !strings.HasPrefix(out, "Sent.") {
		t.Fatalf("status after the chat took the answer: %q", out)
	}
	if w.currentAsk() != nil {
		t.Fatal("the card stayed after the answer was taken")
	}
	ledger := w.askLedger()
	ledger.mu.Lock()
	rec := ledger.records[id]
	ledger.mu.Unlock()
	if rec == nil || rec.Status != askStatusAnswered || rec.RequestReceipt == nil || rec.RequestReceipt.DeliveryState != "admitted" {
		t.Fatalf("receipt not admitted: %+v", rec)
	}
}

// The same for a message the Board typed itself.
func TestSayNotTakenYetStatusIsReplacedWhenSubmitted(t *testing.T) {
	w, f := claudeFakeWorkspace(t, "mac_frame_confirm_unsent.txt")
	if _, err := w.typeIntoChat(context.Background(), "%7", "confirm all"); err != nil {
		t.Fatal(err)
	}
	if out := stOutput(w); !strings.Contains(out, "has not taken it yet") {
		t.Fatalf("status %q", out)
	}
	f.mu.Lock()
	f.busy = false
	f.mu.Unlock()
	w.submitOwnLeftover(context.Background(), "%7", false)
	if out := stOutput(w); out != "Sent." {
		t.Fatalf("status after submit: %q", out)
	}
}

// The provider takes the paste on its own: the input is empty and a turn is
// running. The waiting status goes away just the same.
func TestSayNotTakenYetStatusIsReplacedWhenProviderTookIt(t *testing.T) {
	w, f := claudeFakeWorkspace(t, "mac_frame_confirm_unsent.txt")
	if _, err := w.typeIntoChat(context.Background(), "%7", "confirm all"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.pasted = ""
	f.suggestion = "❯ confirm all\n\n● Working on it…\n\n────────────────────\n❯ \n────────────────────\n"
	f.mu.Unlock()
	w.submitOwnLeftover(context.Background(), "%7", false)
	if out := stOutput(w); strings.Contains(out, "has not taken it yet") {
		t.Fatalf("status still says the message waits: %q", out)
	}
}

// The focus view switches to a card that was not the one focused; its keys
// are bound to the card drawn, while a card changing in place still refuses.
func TestDecisionFocusRebindsToTheNewCardAtRender(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	defer v2Cards.Delete(w)
	dvInbox(w, "proxy.golang.org")
	if !dvKey(w, 'o') || w.boardUI.decisionFocus != focusDecision {
		t.Fatal("o did not focus the decision")
	}
	// A different card takes the place of the focused one.
	w.permissions.mu.Lock()
	w.permissions.inbox = []permission.Request{{Kind: "network", Object: "example.org", Reason: "x", RunID: "R-2", TaskID: "T04"}}
	w.permissions.mu.Unlock()
	d, ok := w.v2DecisionNow(time.Now())
	if !ok || d.key == w.boardUI.decisionKey {
		t.Fatalf("the card did not change: %+v", d)
	}
	// Without a render the old binding refuses the key, protecting the owner.
	// With the new card drawn the key acts on it.
	w.paintHome(boardVisual{theme: NewTheme(ThemeNoColor, true, true), now: time.Now()}, 120, 40)
	if w.boardUI.decisionRev != w.decisionRevision(d) {
		t.Fatalf("focus rev %q, card %q", w.boardUI.decisionRev, w.decisionRevision(d))
	}
	if !dvKey(w, 'a') || !w.inboxView.Confirming() {
		t.Fatalf("the visible card refused its key: %q", dvOutput(w))
	}
	if strings.Contains(dvOutput(w), "That card changed") {
		t.Fatalf("answer %q", dvOutput(w))
	}
}

// After the owner answers the trust prompt in the chat, the waiting line
// leaves the footer.
func TestTrustWaitingLineClearsWhenPromptIsGone(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	oldPoll, oldSettle := sayPoll, saySettle
	sayPoll, saySettle = 20*time.Millisecond, 20*time.Millisecond
	defer func() { sayPoll, saySettle = oldPoll, oldSettle }()
	var frame = "Do you trust the contents of this project?\n\n❯ Yes, I trust the contents of this project\n  No, exit\n"
	var mu = make(chan struct{}, 1)
	mu <- struct{}{}
	run := func(_ context.Context, args ...string) ([]byte, error) {
		<-mu
		defer func() { mu <- struct{}{} }()
		switch args[0] {
		case "display-message":
			return []byte("%7 0 0\n"), nil
		case "capture-pane":
			return []byte(frame), nil
		}
		return nil, nil
	}
	w.chatRun = run
	w.tmuxMu.Lock()
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", paneID: "%7", window: "marshal-chat-fake", provider: "claude"}
	w.tmuxMu.Unlock()
	w.sayToMarshal(context.Background(), "hello")
	stWait(t, func() bool {
		return strings.Contains(stOutput(w), "asks to trust this folder")
	}, func() string { return stOutput(w) })
	<-mu
	frame = "Welcome\n\n> \n"
	mu <- struct{}{}
	stWait(t, func() bool {
		return !strings.Contains(stOutput(w), "asks to trust this folder")
	}, func() string { return stOutput(w) })
}

func stOutput(w *Workspace) string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.state.LastOutput
}

func stWait(t *testing.T, ok func() bool, why func() string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", why())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
