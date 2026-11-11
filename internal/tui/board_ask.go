package tui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

// Ask cards: a question the Marshal asks the owner, with one key or one
// click per answer. The Marshal writes an "ask" proposal; the Board shows
// it in the inbox as a card with the choices; a key press or a click sends
// the chosen choice's label to the docked chat as the owner's own message
// (board_say.go), which the Marshal reads like any typed line. A card never
// accepts or approves anything; its only effect is the owner's words in the
// chat. Cards are not persisted across restarts: the question is still in
// the chat, and the Marshal re-asks on Continue.

// askChoice is one answer the owner can pick: Key is "enter" for the first
// (recommended) choice, otherwise one lowercase ASCII letter.
type askChoice struct {
	Key, Label string
}

// askCard is the question the Marshal is waiting on, with its choices.
type askCard struct {
	Question string
	Choices  []askChoice
	At       time.Time
}

// errAskChoices is the one error parseAskChoices returns: the proposal is
// refused as invalid, like any other malformed Marshal proposal.
var errAskChoices = errors.New("invalid ask choices")

// parseAskChoices reads the proposal's choices array: 2 to 4 objects with
// exactly the string keys "key" and "label". The first key is "enter"; every
// other key is one lowercase ASCII letter, unique and not "q". Each label is
// trimmed non-empty, at most 40 bytes, and unique across the choices.
func parseAskChoices(s string) ([]askChoice, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	var raw []struct {
		Key   string `json:"key"`
		Label string `json:"label"`
	}
	if err := dec.Decode(&raw); err != nil {
		return nil, errAskChoices
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errAskChoices
	}
	if len(raw) < 2 || len(raw) > 4 {
		return nil, errAskChoices
	}
	keys := map[string]bool{}
	labels := map[string]bool{}
	out := make([]askChoice, 0, len(raw))
	for i, c := range raw {
		if i == 0 {
			if c.Key != "enter" {
				return nil, errAskChoices
			}
		} else {
			if len(c.Key) != 1 || c.Key[0] < 'a' || c.Key[0] > 'z' || c.Key == "q" {
				return nil, errAskChoices
			}
		}
		if keys[c.Key] {
			return nil, errAskChoices
		}
		keys[c.Key] = true
		if strings.TrimSpace(c.Label) == "" || len(c.Label) > 40 {
			return nil, errAskChoices
		}
		if labels[c.Label] {
			return nil, errAskChoices
		}
		labels[c.Label] = true
		out = append(out, askChoice{Key: c.Key, Label: c.Label})
	}
	return out, nil
}

// openAsk shows the ask card, replacing any card still open: one open
// question at a time. It records the question in Activity and resolves the
// stored proposal as applied, like intake. No popup.
func (w *Workspace) openAsk(p marshalProposal) {
	choices, err := parseAskChoices(p.choices)
	if err != nil {
		return
	}
	w.ask.mu.Lock()
	w.ask.card = &askCard{Question: strings.TrimSpace(p.question), Choices: choices, At: time.Now()}
	w.ask.mu.Unlock()
	w.RecordActivity("Marshal asks: " + p.question)
	if w.store != nil {
		_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, p.occurrence, "applied")
	}
	w.requestRepaint()
}

// currentAsk is the open card, nil for none.
func (w *Workspace) currentAsk() *askCard {
	w.ask.mu.Lock()
	defer w.ask.mu.Unlock()
	return w.ask.card
}

// clearAsk closes the open card.
func (w *Workspace) clearAsk() {
	w.ask.mu.Lock()
	w.ask.card = nil
	w.ask.mu.Unlock()
	w.requestRepaint()
}

// handleAskKey is the ask card's keys on the Board, taken before the gate
// keys while a card is open and no body view or inspector covers the Board:
// Enter picks the first (recommended) choice, each letter picks its choice.
// Every other key, the digits and q, ?, Space and Ctrl+X among them, falls
// through to keep its meaning.
func (w *Workspace) handleAskKey(event KeyEvent) bool {
	card := w.currentAsk()
	if card == nil || w.anyBodyView() || w.boardUI.inspect != "" {
		return false
	}
	if event.Type == KeyEnter {
		w.answerAsk(0)
		return true
	}
	if event.Type != KeyRune {
		return false
	}
	for i, c := range card.Choices {
		if i == 0 {
			continue
		}
		if len(c.Key) == 1 && rune(c.Key[0]) == event.Rune {
			w.answerAsk(i)
			return true
		}
	}
	return false
}

// answerAsk sends choice i's label to the Marshal chat as the owner's own
// message and closes the card. There is no arm/confirm step: these are
// answers, not acceptances. With no chat open the card stays and nothing is
// sent.
func (w *Workspace) answerAsk(i int) {
	card := w.currentAsk()
	if card == nil || i < 0 || i >= len(card.Choices) {
		return
	}
	if w.sayPane() == "" && !w.nativeChatShown() {
		w.setOutput("No Marshal chat is open; the answer was not sent.", true)
		return
	}
	label := card.Choices[i].Label
	w.clearAsk()
	w.sayToMarshal(w.workers.context(), label)
	w.RecordActivity("You answered: " + label)
}

// askEntry is the open card as the inbox shows it: the question on at most
// two rows, truncated with an ellipsis, then a row of choices. x is the
// inbox's first cell, row its frame row.
func (v boardVisual) askEntry(c askCard, x, row, w int) []string {
	flag := "⚑ "
	if !v.unicode() {
		flag = "! "
	}
	head := v.fg("waiting", flag+"question") + v.fg("dim", " · ")
	prefix := VisibleLen(head)
	lines := wrapWords(cleanBoard(c.Question), max(8, w-prefix))
	if len(lines) == 0 {
		lines = []string{""}
	}
	out := []string{PadCell(head+v.bold(v.fg("white", lines[0])), w)}
	if len(lines) > 1 {
		second := lines[1]
		if len(lines) > 2 {
			second = v.trunc(strings.Join(lines[1:], " "), max(8, w-prefix))
		}
		out = append(out, PadCell(strings.Repeat(" ", prefix)+v.bold(v.fg("white", second)), w))
	}
	crow := row + len(out)
	line, at := "    ", 4
	for i, ch := range c.Choices {
		if i > 0 {
			line += "  "
			at += 2
		}
		key := ch.Key
		if key == "enter" {
			key = "Enter"
		}
		button := v.bg("rule", "["+key+"]") + " " + ch.Label
		v.hit(boardHit{row: crow, x0: x + at, x1: x + min(w, at+VisibleLen(button)), kind: hitAsk, id: strconv.Itoa(i)})
		line += button
		at += VisibleLen(button)
	}
	out = append(out, PadCell(line, w))
	return out
}

// askCount is 1 while a card is open, for the inbox's counts.
func (v boardVisual) askCount() int {
	if v.ask != nil {
		return 1
	}
	return 0
}
