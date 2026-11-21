package tui

import (
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/redaction"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// marshalSaid is the final text of the Marshal's last chat turn: assistant
// prose only (never a tool call or a tool's output), scrubbed of secrets.
// Answered is set once the owner (or the Board) wrote in the chat after it.
type marshalSaid struct {
	text     string
	at       time.Time
	answered bool
}

// noteMarshalSaid keeps the bound Marshal conversation's last turn. It is
// called by the consumer of the bound live Marshal chat only (the same one
// that reads its proposals), with a transcript or a batch of new messages:
// a batch with no prose keeps what was there.
func (w *Workspace) noteMarshalSaid(tr importer.SessionTranscript) {
	lastSaid, lastAsked := -1, -1
	for i, m := range tr.Messages {
		if m.Kind != importer.MessageKindText || strings.TrimSpace(m.Content) == "" {
			continue
		}
		switch m.Role {
		case "assistant":
			if prose := marshalProse(m.Content); prose != "" {
				lastSaid = i
			}
		case "user":
			lastAsked = i
		}
	}
	switch {
	case lastSaid >= 0 && lastSaid > lastAsked:
		m := tr.Messages[lastSaid]
		w.noteMarshalText(m.Content, m.Timestamp)
	case lastAsked >= 0:
		w.markMarshalAnswered()
	}
}

// noteMarshalText keeps text as the Marshal's last turn (the native chat
// calls it with each final text segment). A text seen before keeps its
// first time and its answered state, so polling the same history does not
// move it.
func (w *Workspace) noteMarshalText(text string, at time.Time) {
	prose := marshalProse(text)
	if prose == "" {
		return
	}
	if prev := w.marshalSaid.Load(); prev != nil && prev.text == prose {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}
	w.marshalSaid.Store(&marshalSaid{text: prose, at: at})
}

// markMarshalAnswered notes that someone wrote in the chat after the
// Marshal's last turn: the owner, or the Board on the owner's behalf.
func (w *Workspace) markMarshalAnswered() {
	if prev := w.marshalSaid.Load(); prev != nil && !prev.answered {
		next := *prev
		next.answered = true
		w.marshalSaid.Store(&next)
	}
}

// marshalProse is an assistant message as the owner may read it: proposal
// protocol lines out, the Marshal protocol and every secret the detectors
// know redacted.
func marshalProse(text string) string {
	var keep []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "MARSHAL_PROPOSAL") {
			continue
		}
		keep = append(keep, line)
	}
	out := strings.TrimSpace(strings.Join(keep, "\n"))
	if out == "" {
		return ""
	}
	return redaction.RedactAll(RedactContent(out, nil))
}

// collectV2MarshalSays fills b.MarshalSays from the last turn kept.
func (w *Workspace) collectV2MarshalSays(b *vm.Board) {
	if s := w.marshalSaid.Load(); s != nil {
		b.MarshalSays = vm.MarshalSays{Text: s.text, At: s.at, Answered: s.answered}
	}
}
