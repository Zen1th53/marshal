package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

// closedPanel is a published panel whose run head has the given state and
// counts, as marshalPublish hands noteRunClosed.
func closedPanel(runID string, state marshal.RunState, done, total int, limit, tokens int64) *MarshalPanel {
	head := boardmodel.RunHead{ID: runID, State: state, Done: done, Total: total, TokenLimit: limit, Tokens: marshal.Amount{Value: tokens, Known: true}}
	return &MarshalPanel{RunID: runID, State: state, Board: boardmodel.Board{Run: head}}
}

// A Verifying → Closed transition for a run shows the card once, built from
// the run head. A re-publish of the closed run, after the owner closes the
// card with n, does not re-open it.
func TestNextCardOnClose(t *testing.T) {
	w := emptyBoardWorkspace(t)
	prev := closedPanel("RUN-1", marshal.Verifying, 4, 4, 0, 0)
	cur := closedPanel("RUN-1", marshal.Closed, 4, 4, 0, 0)
	w.noteRunClosed(prev, cur)
	c := w.currentNext()
	if c == nil {
		t.Fatal("the card did not open on Verifying → Closed")
	}
	if c.RunID != "RUN-1" || c.Done != 4 || c.Total != 4 || c.HasBudget {
		t.Fatalf("card built wrong: %+v", c)
	}
	// The card leads the first-use inbox and the header says NEEDS YOU.
	text := frameText(w, 120, 40)
	// Row 0 is the vitals row; the Board header is row 1.
	head := strings.SplitN(text, "\n", 3)[1]
	if !strings.Contains(head, "NEEDS YOU") {
		t.Fatalf("header does not say NEEDS YOU: %q", head)
	}
	for _, want := range []string{
		"done · Done: 4 of 4 tasks verified and merged.",
		"[n] new goal",
		"[d] details",
		"[q] quit",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the card misses %q:\n%s", want, text)
		}
	}
	// The owner closes the card with n; a re-publish of the closed run does
	// not re-open it.
	w.clearNext()
	w.noteRunClosed(cur, cur)
	if w.currentNext() != nil {
		t.Fatal("the card re-opened on a re-published Closed run")
	}
}

// Starting the TUI with an already-closed latest run shows no card: the first
// publish of a closed run has no non-closed predecessor here.
func TestNextCardNotOnStartupClosedRun(t *testing.T) {
	w := emptyBoardWorkspace(t)
	cur := closedPanel("RUN-1", marshal.Closed, 2, 2, 0, 0)
	// No previous panel: the recovered run is published closed straight away.
	w.noteRunClosed(nil, cur)
	if w.currentNext() != nil {
		t.Fatal("a card opened for an already-closed startup run")
	}
	// A previous panel for a different run is not this run's predecessor.
	w.noteRunClosed(closedPanel("RUN-0", marshal.Verifying, 1, 1, 0, 0), cur)
	if w.currentNext() != nil {
		t.Fatal("a card opened although no non-closed predecessor of this run was published")
	}
}

// n focuses the Marshal chat (as m does) and closes the card; d opens the
// Verify view and leaves the card up.
func TestNextCardKeys(t *testing.T) {
	w, _, _ := boardWithNativeChat(t)
	w.verSource = &fakeVerifyView{overview: demoVerifyOverview(time.Now())}
	w.openNext(boardmodel.RunHead{ID: "run", State: marshal.Closed, Done: 1, Total: 1})
	if w.currentNext() == nil {
		t.Fatal("the card did not open")
	}
	// d opens the Verify view; the card stays.
	if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'd'}) {
		t.Fatal("d was not taken by the card")
	}
	if !w.verView.Load() {
		t.Fatal("d did not open the Verify view")
	}
	if w.currentNext() == nil {
		t.Fatal("the card closed when d opened the view")
	}
	// n focuses the chat and closes the card.
	if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'n'}) {
		t.Fatal("n was not taken by the card")
	}
	if w.currentNext() != nil {
		t.Fatal("the card stayed open after n")
	}
	if !w.nativeChatFocused() {
		t.Fatal("n did not focus the Marshal chat")
	}
}

// The card states runtime counts: Not done only when Done < Total, Budget
// only with a token ceiling.
func TestNextCardText(t *testing.T) {
	cases := []struct {
		name string
		card nextCard
		want string
	}{
		{"all done, no limit", nextCard{Done: 4, Total: 4}, "Done: 4 of 4 tasks verified and merged."},
		{"not all done, with budget", nextCard{Done: 3, Total: 4, HasBudget: true, Budget: 38}, "Done: 3 of 4 tasks verified and merged. Not done: 1. Budget 38%."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.card.line(); got != tc.want {
				t.Fatalf("line() = %q, want %q", got, tc.want)
			}
		})
	}
}

// newNextCard reads the counts off the run head: Budget is the rounded
// percent of the token ceiling, shown only when the plan set one.
func TestNextCardFromHead(t *testing.T) {
	c := newNextCard(boardmodel.RunHead{ID: "R", Done: 3, Total: 4, TokenLimit: 1000, Tokens: marshal.Amount{Value: 380, Known: true}})
	if !c.HasBudget || c.Budget != 38 {
		t.Fatalf("budget from head wrong: %+v", c)
	}
	if got := c.line(); got != "Done: 3 of 4 tasks verified and merged. Not done: 1. Budget 38%." {
		t.Fatalf("line() = %q", got)
	}
	// Rounding: 375/1000 rounds to 38, not 37.
	if c := newNextCard(boardmodel.RunHead{TokenLimit: 1000, Tokens: marshal.Amount{Value: 375, Known: true}}); c.Budget != 38 {
		t.Fatalf("rounding wrong: %d", c.Budget)
	}
	// No ceiling: no budget.
	if c := newNextCard(boardmodel.RunHead{Done: 1, Total: 1}); c.HasBudget {
		t.Fatalf("budget shown with no ceiling: %+v", c)
	}
}

// With an ask card also open, the "what next?" card renders above it and
// takes n first; Enter still reaches the ask card, which stays open until then.
func TestNextCardBeforeAskCard(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	ap, err := parseMarshalProposal(askProposalLine(exampleAskQuestion, exampleAskChoices))
	if err != nil {
		t.Fatal(err)
	}
	w.openAsk(ap)
	w.noteRunClosed(closedPanel("RUN-1", marshal.Verifying, 4, 4, 0, 0), closedPanel("RUN-1", marshal.Closed, 4, 4, 0, 0))
	if w.currentNext() == nil || w.currentAsk() == nil {
		t.Fatal("both cards should be open")
	}
	text := frameText(w, 160, 60)
	next := strings.Index(text, "[n] new goal")
	ask := strings.Index(text, "question · "+exampleAskQuestion)
	if next < 0 || ask < 0 || next > ask {
		t.Fatalf("the next card should render above the ask card (next %d, ask %d):\n%s", next, ask, text)
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'n'}) {
		t.Fatal("n was not taken")
	}
	if w.currentNext() != nil {
		t.Fatal("n did not close the next card")
	}
	if w.currentAsk() == nil {
		t.Fatal("n closed the ask card")
	}
}
