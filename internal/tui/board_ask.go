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
	// digest identifies this question and its choices so an already-answered
	// ask proposal is not shown again after a control-centre restart.
	digest string
	// guard is the changed-files alert this question decides, nil when it
	// is about something else (askAboutGuard): answering one resolves or
	// updates the other.
	guard *guardAlert
}

// askCardDigest hashes the normalized question and choices of an ask card.
func askCardDigest(question string, choices []askChoice) string {
	var b strings.Builder
	b.WriteString(strings.Join(strings.Fields(question), " "))
	for _, c := range choices {
		b.WriteByte('\x00')
		b.WriteString(c.Key)
		b.WriteByte('=')
		b.WriteString(strings.TrimSpace(c.Label))
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(b.String())))
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
	question := strings.TrimSpace(p.question)
	card := &askCard{Question: question, Choices: choices, At: time.Now(), digest: askCardDigest(question, choices)}
	if a := w.guardAlertNow(); a != nil && askAboutGuard(card.Question, choices) {
		card.guard = a
	}
	w.ask.mu.Lock()
	w.ask.card = card
	w.ask.mu.Unlock()
	w.RecordActivity("Marshal asks: " + p.question)
	if w.store != nil {
		_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, p.occurrence, "applied")
	}
	w.recordProposalCardLatest("ask")
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
	if card == nil || w.anyBodyView() {
		return false
	}
	if event.Type == KeyEnter {
		if w.boardStopWaiting() {
			// A stopped run leads the inbox: Enter is its key, never a
			// card's below it.
			return false
		}
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
	w.recordAskAnswered(card.digest)
	w.sayToMarshal(w.workers.context(), label)
	w.RecordActivity("You answered: " + label)
	// A question about the changed files answers their card too: keep
	// keeps them; an undo choice leaves the card open, since nothing is
	// undone yet, and the card says what was answered.
	if card.guard != nil {
		switch {
		case askChoiceKeeps(label):
			w.guardKeepAlert(card.guard)
		case askChoiceUndoes(label):
			w.guardAnswered(card.guard, label)
		}
	}
}

// askAboutGuard reports whether a question decides the changed-files alert
// open when it was asked: it is about changes or files, and one of its
// choices keeps or undoes them.
func askAboutGuard(question string, choices []askChoice) bool {
	q := strings.ToLower(question)
	if !strings.Contains(q, "change") && !strings.Contains(q, "file") {
		return false
	}
	for _, c := range choices {
		if askChoiceKeeps(c.Label) || askChoiceUndoes(c.Label) {
			return true
		}
	}
	return false
}

// askChoiceKeeps and askChoiceUndoes read a choice's label as keeping or
// undoing the changed files.
func askChoiceKeeps(label string) bool {
	l := strings.ToLower(label)
	return strings.Contains(l, "keep") && !askChoiceUndoes(label)
}

func askChoiceUndoes(label string) bool {
	l := strings.ToLower(label)
	return strings.Contains(l, "undo") || strings.Contains(l, "revert") || strings.Contains(l, "discard")
}

// askForGuard is the open question about alert a and its choice that keeps
// the files (-1 for none), or nil.
func (w *Workspace) askForGuard(a *guardAlert) (*askCard, int) {
	card := w.currentAsk()
	if card == nil || a == nil || card.guard != a {
		return nil, -1
	}
	for i, c := range card.Choices {
		if askChoiceKeeps(c.Label) {
			return card, i
		}
	}
	return card, -1
}

// askCount is 1 while a card is open, for the inbox's counts.
func (v boardVisual) askCount() int {
	if v.ask != nil {
		return 1
	}
	return 0
}
