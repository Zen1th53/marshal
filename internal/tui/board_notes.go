package tui

import (
	"strings"
	"sync"
	"time"
)

// Background messages on the Board. Anything the operator did not just ask
// for (a re-delivery to the Marshal, an egress alert, a recorded owner
// instruction, a verify result) is one Activity line, never the ANSWER
// overlay: the overlay covers the Board and is for the answer to the
// operator's own command. A click on the line shows the whole text.

// boardNote is one background message.
type boardNote struct {
	id   int
	At   time.Time
	Text string // the whole message, redacted
	Err  bool
}

// boardNotes is the Board's background messages, newest first.
type boardNotes struct {
	mu   sync.Mutex
	list []boardNote
	next int
}

// boardNoteLimit is how many background messages Activity remembers.
const boardNoteLimit = 16

// addNote records a background message for the Board's Activity and
// repaints. An empty message, or one repeating the newest, adds nothing.
func (w *Workspace) addNote(text string, isError bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	n := &w.notes
	n.mu.Lock()
	if len(n.list) > 0 && n.list[0].Text == text && n.list[0].Err == isError {
		n.mu.Unlock()
		return
	}
	n.next++
	n.list = append([]boardNote{{id: n.next, At: time.Now(), Text: text, Err: isError}}, n.list...)
	n.list = n.list[:min(len(n.list), boardNoteLimit)]
	n.mu.Unlock()
	w.requestRepaint()
}

// noteList is the background messages for a paint, newest first.
func (w *Workspace) noteList() []boardNote {
	w.notes.mu.Lock()
	defer w.notes.mu.Unlock()
	return append([]boardNote(nil), w.notes.list...)
}

// openNote shows one background message whole in the ANSWER overlay: the
// operator clicked it, so it is their answer now.
func (w *Workspace) openNote(id int) bool {
	for _, n := range w.noteList() {
		if n.id == id {
			w.memoryAnswer(n.Text, n.Err)
			return true
		}
	}
	return false
}

// noteSummary is a message's one Activity line: its first non-empty line.
func noteSummary(text string) string {
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return cleanBoard(l)
		}
	}
	return ""
}

// noteLine is one background message in Activity: when, then its first
// line, red when it reports a failure, "· click" when there is more.
func (v boardVisual) noteLine(n boardNote, w int) string {
	head := v.fg("dim", n.At.In(v.now.Location()).Format("15:04:05")) + " "
	label, col := "note", "dim"
	if n.Err {
		label, col = "error", "failed"
	}
	head += v.fg(col, label) + " "
	text := noteSummary(n.Text)
	tail := ""
	if strings.Contains(strings.TrimSpace(n.Text), "\n") || VisibleLen(head)+VisibleLen(text) > w {
		// More than the line shows: the click shows it whole.
		tail = v.fg("dim", " · click")
	}
	room := max(8, w-VisibleLen(head)-VisibleLen(tail))
	if n.Err {
		text = v.fg("failed", v.trunc(text, room))
	} else {
		text = v.fg("white", v.trunc(text, room))
	}
	return head + text + tail
}
