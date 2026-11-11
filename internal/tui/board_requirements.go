package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
)

// Requirements cards: the a–h project requirements the Marshal fills from the
// code and memory, shown on one card with the source of each line. The Marshal
// writes a "requirements" proposal; the Board shows it as the first inbox
// entry; the owner presses Enter to confirm every line or e to edit one. A
// card never records the requirements of record (the Marshal writes those to
// REQUIREMENTS.md in the plan pack); its only effect is the owner's words in
// the chat, sent through the same say path as a typed line (board_say.go).

// reqLabels is the fixed English caption of each line a–h, in order.
var reqLabels = [8]string{"scope", "don't touch", "branches", "done when", "must still pass", "may change", "deps · network", "budget"}

// reqLine is one line of the card: its letter a–h, the filled text (empty for
// a blank), and where the text came from (empty for a blank).
type reqLine struct {
	Line, Text, Source string
}

// reqCard is the open requirements card. editing is set while the owner has
// pressed e and the card waits for the line letter to edit.
type reqCard struct {
	Lines   [8]reqLine
	editing bool
	At      time.Time
}

// errReqCard is the one error parseRequirementsCard returns: the proposal is
// refused as invalid, like any other malformed Marshal proposal.
var errReqCard = errors.New("invalid requirements card")

// parseRequirementsCard reads the proposal's card array: exactly 8 objects
// with exactly the string keys line, text and source. The line values are
// a..h in that order. text is at most 200 bytes and source at most 80, with
// no control characters. A blank line has an empty text and then an empty
// source; a filled line has a non-empty source.
func parseRequirementsCard(s string) ([8]reqLine, error) {
	var out [8]reqLine
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	var raw []struct {
		Line   string `json:"line"`
		Text   string `json:"text"`
		Source string `json:"source"`
	}
	if err := dec.Decode(&raw); err != nil {
		return out, errReqCard
	}
	if _, err := dec.Token(); err != io.EOF {
		return out, errReqCard
	}
	if len(raw) != 8 {
		return out, errReqCard
	}
	for i, c := range raw {
		if c.Line != string(rune('a'+i)) {
			return out, errReqCard
		}
		if len(c.Text) > 200 || len(c.Source) > 80 {
			return out, errReqCard
		}
		if strings.ContainsFunc(c.Text, unicode.IsControl) || strings.ContainsFunc(c.Source, unicode.IsControl) {
			return out, errReqCard
		}
		if c.Text == "" {
			if c.Source != "" {
				return out, errReqCard
			}
		} else if c.Source == "" {
			return out, errReqCard
		}
		out[i] = reqLine{Line: c.Line, Text: c.Text, Source: c.Source}
	}
	return out, nil
}

// reqFilled is how many of the eight lines are filled.
func (c reqCard) filled() int {
	n := 0
	for _, l := range c.Lines {
		if l.Text != "" {
			n++
		}
	}
	return n
}

// openRequirements shows the card, replacing any card still open and closing
// any open ask card. It records the update in Activity and resolves the stored
// proposal as applied, like intake. No popup.
func (w *Workspace) openRequirements(p marshalProposal) {
	lines, err := parseRequirementsCard(p.card)
	if err != nil {
		return
	}
	card := &reqCard{Lines: lines, At: time.Now()}
	w.req.mu.Lock()
	w.req.card = card
	w.req.mu.Unlock()
	w.clearAsk()
	w.RecordActivity(fmt.Sprintf("Marshal: requirements card updated (%d of 8 filled).", card.filled()))
	if w.store != nil {
		_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, p.occurrence, "applied")
	}
	w.requestRepaint()
}

// currentReq is the open card, nil for none.
func (w *Workspace) currentReq() *reqCard {
	w.req.mu.Lock()
	defer w.req.mu.Unlock()
	return w.req.card
}

// clearReq closes the open card.
func (w *Workspace) clearReq() {
	w.req.mu.Lock()
	w.req.card = nil
	w.req.mu.Unlock()
	w.requestRepaint()
}

// setReqEditing turns the card's "which line?" prompt on or off.
func (w *Workspace) setReqEditing(on bool) {
	w.req.mu.Lock()
	if w.req.card != nil {
		w.req.card.editing = on
	}
	w.req.mu.Unlock()
	w.requestRepaint()
}

// handleRequirementsKey is the card's keys on the Board, taken after the ask
// card's keys and before the gate keys while a card is open and no body view
// or inspector covers the Board. With a card waiting for a line letter (after
// e), a..h opens the composer to edit that line; Esc or any other key cancels
// back. Otherwise Enter confirms (or refuses while blanks remain) and e starts
// an edit. Every other key falls through to keep its meaning.
func (w *Workspace) handleRequirementsKey(event KeyEvent) bool {
	card := w.currentReq()
	if card == nil || w.anyBodyView() || w.boardUI.inspect != "" {
		return false
	}
	if card.editing {
		if event.Type == KeyRune && event.Rune >= 'a' && event.Rune <= 'h' {
			w.beginRequirementsEdit(byte(event.Rune))
			return true
		}
		// Esc or anything else cancels back to the normal keys.
		w.setReqEditing(false)
		return true
	}
	switch event.Type {
	case KeyEnter:
		w.confirmRequirements()
		return true
	case KeyRune:
		if event.Rune == 'e' {
			w.setReqEditing(true)
			return true
		}
	}
	return false
}

// confirmRequirements sends the owner's words "confirm all" when every line is
// filled, and refuses with a one-line note while any blank remains. It never
// confirms on the Marshal's behalf: it only sends the owner's own message.
func (w *Workspace) confirmRequirements() {
	card := w.currentReq()
	if card == nil {
		return
	}
	if card.filled() < 8 {
		w.setOutput("Answer the blank lines first; the Marshal asks them one at a time.", false)
		return
	}
	w.clearReq()
	w.sayToMarshal(w.workers.context(), "confirm all")
	w.RecordActivity("You confirmed the requirements.")
}

// beginRequirementsEdit prefills the composer with the chosen line's current
// text and opens it, leaving the card open; the existing say path sends the
// owner's edit on Enter.
func (w *Workspace) beginRequirementsEdit(letter byte) {
	card := w.currentReq()
	if card == nil || letter < 'a' || letter > 'h' {
		return
	}
	w.setReqEditing(false)
	text := card.Lines[letter-'a'].Text
	w.composer.SetText(fmt.Sprintf("edit %c: %s", letter, text))
	w.openSay()
}

// clickRequirements is a click on a card region: the Enter button confirms,
// the e button starts an edit, and a line row does what e then that letter do.
func (w *Workspace) clickRequirements(id string) {
	switch id {
	case "enter":
		w.confirmRequirements()
	case "edit":
		w.setReqEditing(true)
	default:
		if len(id) == 1 && id[0] >= 'a' && id[0] <= 'h' {
			w.beginRequirementsEdit(id[0])
		}
	}
}

// reqCount is 1 while a card is open, for the inbox's counts.
func (v boardVisual) reqCount() int {
	if v.req != nil {
		return 1
	}
	return 0
}

// truncLeft keeps the right end of s and marks the cut with a leading ellipsis.
func (v boardVisual) truncLeft(s string, w int) string {
	if VisibleLen(s) <= w {
		return s
	}
	ell := "…"
	if !v.unicode() {
		ell = "."
	}
	r := []rune(s)
	for len(r) > 0 && VisibleLen(string(r))+1 > w {
		r = r[1:]
	}
	return ell + string(r)
}

// requirementsEntry is the open card as the inbox shows it: a header with the
// filled count, one row per line a–h with its label, text and source, and the
// action row. x is the inbox's first cell, row its frame row, w its width.
func (v boardVisual) requirementsEntry(c reqCard, x, row, w int) []string {
	flag := "⚑ "
	if !v.unicode() {
		flag = "! "
	}
	head := v.fg("waiting", flag+"requirements") + v.fg("dim", fmt.Sprintf(" · %d of 8 filled", c.filled()))
	out := []string{PadCell(head, w)}

	labelW := 0
	for _, l := range reqLabels {
		if n := VisibleLen(l); n > labelW {
			labelW = n
		}
	}
	for i, line := range c.Lines {
		crow := row + len(out)
		label := reqLabels[i]
		label += strings.Repeat(" ", labelW-VisibleLen(label))
		left := "  " + v.fg("white", line.Line) + "  " + v.fg("dim", label) + "  "
		leftW := 2 + 1 + 2 + labelW + 2
		text, source := line.Text, line.Source
		blank := text == ""
		if blank {
			text = "—"
			source = "(to ask)"
			if !v.unicode() {
				text = "-"
			}
		}
		avail := max(1, w-leftW)
		srcW := VisibleLen(source)
		if srcW+2 > avail {
			srcW = max(1, avail-10)
			source = v.truncLeft(source, srcW)
			srcW = VisibleLen(source)
		}
		textBudget := max(1, avail-srcW-2)
		text = v.trunc(text, textBudget)
		pad := max(1, avail-VisibleLen(text)-srcW)
		textCell := v.fg("white", text)
		if blank {
			textCell = v.fg("dim", text)
		}
		rowStr := left + textCell + strings.Repeat(" ", pad) + v.fg("dim", source)
		v.hit(boardHit{row: crow, x0: x, x1: x + w, kind: hitRequirements, id: line.Line})
		out = append(out, PadCell(rowStr, w))
	}

	arow := row + len(out)
	if c.editing {
		dash := "–"
		if !v.unicode() {
			dash = "-"
		}
		line := "  " + v.fg("waiting", "Which line? ") + v.bg("rule", "[a]") + v.fg("dim", dash) + v.bg("rule", "[h]") + "  " + v.bg("rule", "[Esc]") + v.fg("dim", " cancel")
		out = append(out, PadCell(line, w))
		return out
	}
	confirm := v.bg("rule", "[Enter]") + " confirm all"
	edit := v.bg("rule", "[e]") + " edit a line"
	line := "  " + confirm + "  " + edit
	v.hit(boardHit{row: arow, x0: x + 2, x1: x + 2 + VisibleLen(confirm), kind: hitRequirements, id: "enter"})
	v.hit(boardHit{row: arow, x0: x + 2 + VisibleLen(confirm) + 2, x1: x + 2 + VisibleLen(confirm) + 2 + VisibleLen(edit), kind: hitRequirements, id: "edit"})
	out = append(out, PadCell(line, w))
	return out
}
