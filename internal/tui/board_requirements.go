package tui

import (
	"context"
	"crypto/sha256"
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
	texts := reqTexts(lines)
	confirmedBefore := w.requirementsConfirmedInStore(texts)
	card := &reqCard{Lines: lines, At: time.Now()}
	w.req.mu.Lock()
	again := (w.req.confirmed != nil && *w.req.confirmed == texts) || confirmedBefore
	if again && w.req.confirmed == nil {
		c := texts
		w.req.confirmed = &c
	}
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
	w.recordProposalCardLatest("requirements")
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

// reqTextsDigest hashes the normalized requirement texts so a confirmation
// survives control-centre restarts.
func reqTextsDigest(texts [8]string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(texts[:], "\x00"))))
}

// currentReq is a copy of the open card, nil for none: a paint or a key
// reads it without holding w.req.mu while the card changes.
func (w *Workspace) currentReq() *reqCard {
	w.req.mu.Lock()
	defer w.req.mu.Unlock()
	if w.req.card == nil {
		return nil
	}
	c := *w.req.card
	return &c
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
	w.recordRequirementsConfirmed(texts)
	w.recordProposalCardLatest("requirements")
	w.clearReq()
	w.sayToMarshal(w.workers.context(), "confirm all")
	w.RecordActivity("You confirmed the requirements.")
	w.offerDeferredArchitecture()
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
