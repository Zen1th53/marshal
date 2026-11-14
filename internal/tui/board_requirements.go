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

// errReqCard is what every parseRequirementsCard error wraps: the proposal is
// refused as invalid, like any other malformed Marshal proposal. The wrapped
// message names the line and the rule it broke, so the Marshal can correct
// that line and send the whole card again (a card is always sent whole: it is
// how a line already shown is updated).
var errReqCard = errors.New("invalid requirements card")

func reqCardError(format string, args ...any) error {
	return fmt.Errorf("%w: "+format+"; fix it and send the whole card again", append([]any{errReqCard}, args...)...)
}

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
		return out, reqCardError("card must be a JSON array, sent as a string, of objects with only line, text and source")
	}
	if _, err := dec.Token(); err != io.EOF {
		return out, reqCardError("card has data after its array")
	}
	if len(raw) != 8 {
		return out, reqCardError("card has %d lines, it needs all 8 (a to h)", len(raw))
	}
	for i, c := range raw {
		want := string(rune('a' + i))
		if c.Line != want {
			return out, reqCardError("line %d is %q, want %q (lines a to h in order)", i+1, c.Line, want)
		}
		if len(c.Text) > 200 {
			return out, reqCardError("line %s text is %d bytes, at most 200", want, len(c.Text))
		}
		if len(c.Source) > 80 {
			return out, reqCardError("line %s source is %d bytes, at most 80", want, len(c.Source))
		}
		if strings.ContainsFunc(c.Text, unicode.IsControl) || strings.ContainsFunc(c.Source, unicode.IsControl) {
			return out, reqCardError("line %s has a control character", want)
		}
		if c.Text == "" {
			if c.Source != "" {
				return out, reqCardError("line %s is blank but has a source; a blank line has text and source both empty", want)
			}
		} else if c.Source == "" {
			return out, reqCardError("line %s has text but no source", want)
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
	again := w.req.confirmed != nil && *w.req.confirmed == reqTexts(lines)
	if !again {
		w.req.card = card
	}
	w.req.mu.Unlock()
	if again {
		// The Marshal sent back the card the owner already confirmed,
		// with its sources updated: it is recorded, and no card opens to
		// take a later Enter.
		w.RecordActivity("Marshal recorded the requirements you confirmed.")
		if w.store != nil {
			_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, p.occurrence, "applied")
		}
		w.requestRepaint()
		return
	}
	w.clearAsk()
	w.RecordActivity(fmt.Sprintf("Marshal: requirements card updated (%d of 8 filled).", card.filled()))
	if w.store != nil {
		_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, p.occurrence, "applied")
	}
	w.requestRepaint()
}

// reqTexts is each line's text, spaces collapsed, the way two cards are
// compared: the same requirements with other sources are the same card.
func reqTexts(lines [8]reqLine) [8]string {
	var out [8]string
	for i, l := range lines {
		out[i] = strings.Join(strings.Fields(l.Text), " ")
	}
	return out
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
		if w.boardStopWaiting() {
			// A stopped run leads the inbox: Enter is its key.
			return false
		}
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
	texts := reqTexts(card.Lines)
	w.req.mu.Lock()
	w.req.confirmed = &texts
	w.req.mu.Unlock()
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

// requirementsEntry is the open card as the inbox shows it: a header with the
// filled count, one entry per line a–h with its label, text and source, and the
// action row. x is the inbox's first cell, row its frame row, w its width, and
// maxRows the rows the inbox has left for the card.
//
// The requirement's own words come first: they get the whole width after the
// label and wrap onto up to three rows. The source follows as a dim suffix
// when it fits after the text, or on a dim row of its own below it. When the
// inbox is too short for that, sources that do not fit after the text are
// left out, and then each text keeps to one row, so the action row stays.
func (v boardVisual) requirementsEntry(c reqCard, x, row, w, maxRows int) []string {
	flag := "⚑ "
	if !v.unicode() {
		flag = "! "
	}
	head := v.fg("waiting", flag+"requirements") + v.fg("dim", fmt.Sprintf(" · %d of 8 filled", c.filled()))

	entries := v.reqLayout(c, w, maxRows)
	out := []string{PadCell(head, w)}
	for i, rows := range entries {
		for _, r := range rows {
			v.hit(boardHit{row: row + len(out), x0: x, x1: x + w, kind: hitRequirements, id: c.Lines[i].Line})
			out = append(out, PadCell(r, w))
		}
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
	confirm := v.bg("rule", "["+v.cardEnterKey()+"]") + " confirm all"
	edit := v.bg("rule", "[e]") + " edit a line"
	line := "  " + confirm + "  " + edit
	v.hit(boardHit{row: arow, x0: x + 2, x1: x + 2 + VisibleLen(confirm), kind: hitRequirements, id: "enter"})
	v.hit(boardHit{row: arow, x0: x + 2 + VisibleLen(confirm) + 2, x1: x + 2 + VisibleLen(confirm) + 2 + VisibleLen(edit), kind: hitRequirements, id: "edit"})
	out = append(out, PadCell(line, w))
	return out
}

// reqLayout is the rows of each of the card's eight lines at width w: the
// first layout that fits maxRows with the header and the action row, or the
// shortest. It draws nothing and registers no click region.
func (v boardVisual) reqLayout(c reqCard, w, maxRows int) [8][]string {
	labelW := 0
	for _, l := range reqLabels {
		if n := VisibleLen(l); n > labelW {
			labelW = n
		}
	}
	leftW := 2 + 1 + 2 + labelW + 2
	avail := max(1, w-leftW)
	type pass struct {
		textRows  int
		sourceRow bool
	}
	passes := []pass{{3, true}, {3, false}, {1, false}}
	var entries [8][]string
	for pi, ps := range passes {
		total := 2
		for i, line := range c.Lines {
			entries[i] = v.reqEntryRows(line, i, leftW, labelW, avail, ps.textRows, ps.sourceRow)
			total += len(entries[i])
		}
		if total <= maxRows || pi == len(passes)-1 {
			break
		}
	}
	return entries
}

// reqRows is how many rows the card draws at width w with maxRows to spare.
func (v boardVisual) reqRows(c reqCard, w, maxRows int) int {
	n := 2
	for _, rows := range v.reqLayout(c, w, maxRows) {
		n += len(rows)
	}
	return n
}

// reqEntryRows is one line of the card: the letter and label, the text wrapped
// to at most textRows rows of avail cells, and the source as a dim suffix after
// the text when it fits there, else on a dim row of its own when sourceRow is
// set, else left out.
func (v boardVisual) reqEntryRows(line reqLine, i, leftW, labelW, avail, textRows int, sourceRow bool) []string {
	label := reqLabels[i]
	label += strings.Repeat(" ", labelW-VisibleLen(label))
	left := "  " + v.fg("white", line.Line) + "  " + v.fg("dim", label) + "  "
	indent := strings.Repeat(" ", leftW)
	text, source := line.Text, line.Source
	blank := text == ""
	if blank {
		text = "—"
		source = "(to ask)"
		if !v.unicode() {
			text = "-"
		}
	}
	parts := wrapWords(text, avail)
	if len(parts) == 0 {
		parts = []string{text}
	}
	if len(parts) > textRows {
		rest := strings.Join(parts[textRows-1:], " ")
		parts = append(parts[:textRows-1], rest)
		parts[textRows-1] = v.trunc(parts[textRows-1], avail)
	}
	for k := range parts {
		parts[k] = v.trunc(parts[k], avail)
	}
	color := "white"
	if blank {
		color = "dim"
	}
	var rows []string
	for k, part := range parts {
		lead := indent
		if k == 0 {
			lead = left
		}
		rows = append(rows, lead+v.fg(color, part))
	}
	// The source reads as a dim "· source" after its text.
	dot := "· "
	if !v.unicode() {
		dot = "- "
	}
	source = dot + source
	last := parts[len(parts)-1]
	if room := avail - VisibleLen(last) - 2; VisibleLen(source) <= room {
		rows[len(rows)-1] += "  " + v.fg("dim", source)
	} else if sourceRow {
		rows = append(rows, indent+v.fg("dim", v.trunc(source, avail)))
	}
	return rows
}
