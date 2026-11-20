package tui

import "time"

// Board v2 Activity folds a background message that repeats: the same text
// within foldWindow of its last time is one line with a count, not another
// line. Without it a message sent every 30 s ("Marshal decision queued…",
// "The Marshal did not review a memory import…") filled the 16 notes the
// Board keeps and pushed everything else out.

// foldWindow is how close two equal events must be to fold into one.
const foldWindow = 10 * time.Minute

// foldedNoteLimit is how many folded messages are kept.
const foldedNoteLimit = 32

// foldedNote is one background message with its repeats.
type foldedNote struct {
	Text        string
	Err         bool
	First, Last time.Time
	Count       int
}

// fold records one background message in n.folded, newest first. Callers
// hold n.mu.
func (n *boardNotes) fold(text string, isError bool, at time.Time) {
	for i := range n.folded {
		f := &n.folded[i]
		if f.Text != text || f.Err != isError {
			continue
		}
		if at.Sub(f.Last) > foldWindow {
			break
		}
		f.Count++
		f.Last = at
		moved := *f
		copy(n.folded[1:i+1], n.folded[:i])
		n.folded[0] = moved
		return
	}
	n.folded = append([]foldedNote{{Text: text, Err: isError, First: at, Last: at, Count: 1}}, n.folded...)
	n.folded = n.folded[:min(len(n.folded), foldedNoteLimit)]
}

// foldedNotes is the folded background messages for a frame, newest first.
func (w *Workspace) foldedNotes() []foldedNote {
	w.notes.mu.Lock()
	defer w.notes.mu.Unlock()
	return append([]foldedNote(nil), w.notes.folded...)
}
