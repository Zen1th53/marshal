package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

// The "what next?" card. Protocol v2 step 9: when a run closes, the Marshal's
// report stays in the chat, and the Board shows a short card with the runtime
// counts and the one key that resolves it. The card is built from the run
// head alone — these are facts the Board already holds, never the Marshal's
// words. It opens once, when the run the TUI is watching moves to Closed, and
// is remembered by run id so a later Closed publish does not re-open it.

// nextCard is the "what next?" card after a run closes, built from the run
// head: how many tasks merged, and the budget used when the plan had a
// ceiling.
type nextCard struct {
	RunID     string
	Done      int
	Total     int
	Budget    int  // percent of the token ceiling used, when HasBudget
	HasBudget bool // the plan had a token ceiling
	At        time.Time
}

// newNextCard reads the counts off the run head. Budget is shown only when
// the plan set a token ceiling; its percent rounds 100 * Tokens / TokenLimit
// with the same amount accessor the header uses for Tokens.
func newNextCard(h boardmodel.RunHead) nextCard {
	c := nextCard{RunID: h.ID, Done: h.Done, Total: h.Total, At: time.Now()}
	if h.TokenLimit > 0 {
		c.HasBudget = true
		c.Budget = int(math.Round(float64(h.Tokens.Value) * 100 / float64(h.TokenLimit)))
	}
	return c
}

// line is the one sentence of runtime counts the card states.
func (c nextCard) line() string {
	s := fmt.Sprintf("Done: %d of %d tasks verified and merged.", c.Done, c.Total)
	if c.Done < c.Total {
		s += fmt.Sprintf(" Not done: %d.", c.Total-c.Done)
	}
	if c.HasBudget {
		s += fmt.Sprintf(" Budget %d%%.", c.Budget)
	}
	return s
}

// noteRunClosed opens the card when a run the TUI watched moves to Closed. It
// compares the previously published panel with the new one: the card shows
// only on the transition to Closed for the same run. The first publish of an
// already-closed run — a stored run recovered at startup — has no non-closed
// predecessor here, so it shows no card.
func (w *Workspace) noteRunClosed(prev, cur *MarshalPanel) {
	if cur == nil {
		return
	}
	head := cur.Board.Run
	if head.State != marshal.Closed || head.ID == "" {
		return
	}
	if prev == nil || prev.RunID != cur.RunID || prev.Board.Run.State == marshal.Closed {
		return
	}
	w.openNext(head)
}

// openNext shows the card for the run, once per run id: after the owner
// resolves it with n, a later Closed publish for the same run does not
// re-open it.
func (w *Workspace) openNext(head boardmodel.RunHead) {
	card := newNextCard(head)
	w.next.mu.Lock()
	if w.next.shownFor == head.ID {
		w.next.mu.Unlock()
		return
	}
	w.next.card = &card
	w.next.shownFor = head.ID
	w.next.mu.Unlock()
	w.requestRepaint()
}

// currentNext is the open card, nil for none.
func (w *Workspace) currentNext() *nextCard {
	w.next.mu.Lock()
	defer w.next.mu.Unlock()
	return w.next.card
}

// clearNext closes the open card. The run id it was shown for is remembered,
// so the card does not re-open for that run.
func (w *Workspace) clearNext() {
	w.next.mu.Lock()
	w.next.card = nil
	w.next.mu.Unlock()
	w.requestRepaint()
}

// handleNextKey is the card's keys on the Board, taken before the gate keys
// while the card is open: n focuses the Marshal chat for the next goal (as m
// does) and closes the card; d opens the Verify view and leaves the card up;
// q is the normal quit. The owner types the next goal; nothing auto-starts.
func (w *Workspace) handleNextKey(event KeyEvent) bool {
	if w.currentNext() == nil || event.Type != KeyRune {
		return false
	}
	switch event.Rune {
	case 'n':
		w.clearNext()
		w.focusChatFromBoard(w.workers.context())
		return true
	case 'd':
		w.openNextDetails()
		return true
	case 'q':
		w.requestExit()
		return true
	}
	return false
}

// clickNext is a click on one of the card's buttons: the same effect as its
// key.
func (w *Workspace) clickNext(id string) {
	switch id {
	case "n":
		w.clearNext()
		w.focusChatFromBoard(w.workers.context())
	case "d":
		w.openNextDetails()
	case "q":
		w.requestExit()
	}
}

// openNextDetails switches to the Verify view, the same view the 5 key opens,
// so the owner can read the claims and their evidence. The card stays open.
func (w *Workspace) openNextDetails() {
	if !w.switchBodyView(KeyEvent{Type: KeyRune, Rune: '5'}) {
		w.setOutput("That view has nothing to read in this session.", false)
	}
}

// requestExit asks the workspace to exit, as /quit does.
func (w *Workspace) requestExit() {
	w.mu.Lock()
	w.exitRequested = true
	w.mu.Unlock()
}

// nextEntry is the open card as the inbox shows it: the counts on at most two
// rows, then the "What next?" keys. x is the inbox's first cell, row its
// frame row.
func (v boardVisual) nextEntry(c nextCard, x, row, w int) []string {
	flag := "⚑ "
	if !v.unicode() {
		flag = "! "
	}
	head := v.fg("merged", flag+"done") + v.fg("dim", " · ")
	prefix := VisibleLen(head)
	lines := wrapWords(cleanBoard(c.line()), max(8, w-prefix))
	if len(lines) == 0 {
		lines = []string{""}
	}
	out := []string{PadCell(head+v.bold(v.fg("white", lines[0])), w)}
	for _, extra := range lines[1:] {
		out = append(out, PadCell(strings.Repeat(" ", prefix)+v.bold(v.fg("white", extra)), w))
	}
	crow := row + len(out)
	line := "    " + v.fg("dim", "What next?") + "  "
	at := 4 + VisibleLen("What next?") + 2
	button := func(key, label, id string) {
		b := v.bg("rule", "["+key+"]") + " " + label
		v.hit(boardHit{row: crow, x0: x + at, x1: x + min(w, at+VisibleLen(b)), kind: hitNext, id: id})
		line += b
		at += VisibleLen(b)
	}
	button("n", "new goal", "n")
	line += "  "
	at += 2
	button("d", "details", "d")
	line += "  "
	at += 2
	button("q", "quit", "q")
	out = append(out, PadCell(line, w))
	return out
}

// nextCount is 1 while the card is open, for the inbox's counts.
func (v boardVisual) nextCount() int {
	if v.next != nil {
		return 1
	}
	return 0
}
