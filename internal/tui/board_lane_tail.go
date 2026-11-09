package tui

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/redaction"
	"github.com/Zen1th53/marshal/internal/tmux"
)

const (
	// laneTailRows is how many pane lines a running lane shows.
	laneTailRows = 3
	// laneTailEvery is how often the sampler looks at the worker panes, and
	// the least time between two captures of one pane.
	laneTailEvery = time.Second
	// laneTailMax is how much of a capture is kept, from its end.
	laneTailMax = 64 << 10
	// laneTailTimeout bounds one capture-pane call.
	laneTailTimeout = 2 * time.Second
)

// paneTailer captures the visible text of a worker pane.
type paneTailer interface {
	Capture(paneID string) (string, error)
}

// tmuxTailer is the default paneTailer: tmux capture-pane of the visible
// screen, without scrollback.
type tmuxTailer struct{ ctx context.Context }

func (t tmuxTailer) Capture(paneID string) (string, error) {
	ctx, cancel := context.WithTimeout(t.ctx, laneTailTimeout)
	defer cancel()
	return tmux.CaptureVisiblePane(ctx, paneID)
}

// laneTailState holds the last lines of each running task worker's pane,
// sampled at most once a second per pane while the Board is on screen.
type laneTailState struct {
	mu      sync.Mutex
	tails   map[string][]string  // pane → last lines, redacted and cut
	sampled map[string]time.Time // pane → when it was last captured
	// tailer, now and every are seams for tests; zero means tmux, the
	// wall clock and laneTailEvery.
	tailer paneTailer
	now    func() time.Time
	every  time.Duration
	// done is closed when the sampler started last has stopped.
	done chan struct{}
}

// startLaneTail runs the lane tail sampler until ctx is done.
func (w *Workspace) startLaneTail(ctx context.Context) {
	s := &w.laneTail
	s.mu.Lock()
	tailer, every := s.tailer, s.every
	if tailer == nil {
		tailer = tmuxTailer{ctx}
	}
	if every <= 0 {
		every = laneTailEvery
	}
	done := make(chan struct{})
	s.done = done
	s.mu.Unlock()
	go func() {
		defer close(done)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if ctx.Err() != nil {
				return
			}
			w.sampleLaneTails(tailer)
		}
	}()
}

// laneTailFor is the tail of a running lane's pane: a copy, or nil when the
// pane is unknown, no longer running or its last capture failed.
func (w *Workspace) laneTailFor(paneID string) []string {
	s := &w.laneTail
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.tails[paneID])
}

// laneTailWant is how many rows the lanes pane asks for tails: up to
// laneTailRows for every running lane with a captured tail.
func (w *Workspace) laneTailWant() int {
	s := &w.laneTail
	s.mu.Lock()
	defer s.mu.Unlock()
	want := 0
	for _, t := range s.tails {
		if len(t) > 0 {
			want += laneTailRows
		}
	}
	return want
}

// sampleLaneTails captures each running worker pane that is due and keeps
// its last lines, forgets panes that stopped running, and asks for a repaint
// when anything shown changed.
func (w *Workspace) sampleLaneTails(tailer paneTailer) {
	s := &w.laneTail
	s.mu.Lock()
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	every := s.every
	if every <= 0 {
		every = laneTailEvery
	}
	s.mu.Unlock()

	var panes []string
	if w.boardVisible() {
		panes = w.laneTailPanes()
	}
	width := w.laneTailWidth()
	at := now()

	s.mu.Lock()
	due := make([]string, 0, len(panes))
	for _, p := range panes {
		if last, ok := s.sampled[p]; ok && at.Sub(last) < every {
			continue
		}
		due = append(due, p)
	}
	s.mu.Unlock()

	fresh := make(map[string][]string, len(due))
	for _, p := range due {
		raw, err := tailer.Capture(p)
		if err != nil {
			// Never stale text: a failed capture shows nothing.
			fresh[p] = nil
			continue
		}
		fresh[p] = tailLines(lastBytes(raw, laneTailMax), laneTailRows, width)
	}

	s.mu.Lock()
	if s.tails == nil {
		s.tails = map[string][]string{}
	}
	if s.sampled == nil {
		s.sampled = map[string]time.Time{}
	}
	changed := false
	for p, lines := range fresh {
		s.sampled[p] = at
		if !slices.Equal(s.tails[p], lines) {
			changed = true
		}
		if len(lines) == 0 {
			delete(s.tails, p)
		} else {
			s.tails[p] = lines
		}
	}
	for p := range s.sampled {
		if !slices.Contains(panes, p) {
			delete(s.sampled, p)
			if _, ok := s.tails[p]; ok {
				delete(s.tails, p)
				changed = true
			}
		}
	}
	s.mu.Unlock()
	if changed {
		w.laneMoved()
		w.requestRepaint()
	}
}

// laneTailPanes are the panes of the running task workers of the run on the
// Board, from the same windows boardWorkers reads.
func (w *Workspace) laneTailPanes() []string {
	p := w.marshalPanel()
	if p == nil || p.RunID == "" {
		return nil
	}
	var panes []string
	w.tmuxMu.Lock()
	for _, a := range w.tmuxActiveWins {
		if a == nil || a.role != "task" || a.runID != p.RunID || a.taskID == "" || a.paneID == "" {
			continue
		}
		switch a.state {
		case "done", "failed", "unavailable":
			continue
		}
		panes = append(panes, a.paneID)
	}
	w.tmuxMu.Unlock()
	// A worker that reported itself finished is not running either.
	return slices.DeleteFunc(panes, func(pane string) bool {
		st, ok := w.paneStatusOf(pane)
		return ok && (st.State == "done" || st.State == "error")
	})
}

// laneTailWidth is the width tails are cut to: the terminal less the lane's
// indent, or a 120-column terminal's when there is none.
func (w *Workspace) laneTailWidth() int {
	cols := 120
	w.mu.RLock()
	s := w.screen
	w.mu.RUnlock()
	if s != nil && s.term != nil {
		if c, _ := s.term.GetSize(); c > 0 {
			cols = c
		}
	}
	return max(cols-2, 1)
}

// lastBytes keeps the last limit bytes of s, from the first line that starts
// inside them.
func lastBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	s = s[len(s)-limit:]
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// tailLines is the last n non-blank lines of a pane capture, made safe to
// paint: escapes removed, secrets redacted, tabs expanded, control runes
// blanked and each line cut to width cells with an ellipsis.
func tailLines(raw string, n, width int) []string {
	if n <= 0 || width <= 0 {
		return nil
	}
	text := redaction.RedactContent(stripTerminalEscapes(raw), nil)
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(cleanBoard(expandTabs(line)), " ")
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	for i, line := range out {
		out[i] = Truncate(line, width)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// stripTerminalEscapes removes CSI, OSC, DCS and other escape sequences,
// which cleanBoard alone would leave as visible debris.
func stripTerminalEscapes(s string) string {
	if !strings.ContainsRune(s, '\x1b') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '\x1b' {
			b.WriteByte(s[i])
			i++
			continue
		}
		if i+1 >= len(s) {
			break
		}
		switch s[i+1] {
		case '[': // CSI: parameters, then a final byte 0x40-0x7e.
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			i = min(j+1, len(s))
		case ']', 'P', '_', '^', 'X': // String: ends at BEL or ESC \.
			j := i + 2
			for j < len(s) {
				if s[j] == '\a' {
					j++
					break
				}
				if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
					j += 2
					break
				}
				j++
			}
			i = j
		default: // Two-byte escape, such as ESC 7 or ESC =.
			i += 2
		}
	}
	return b.String()
}

// expandTabs replaces each tab by the spaces to the next 8-cell stop.
func expandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := 8 - col%8
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col += RuneWidth(r)
	}
	return b.String()
}
