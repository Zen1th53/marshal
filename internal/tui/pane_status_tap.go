package tui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/Zen1th53/marshal/internal/tmux"
	"github.com/Zen1th53/marshal/internal/tui/board/widgets/lanes"
)

// A worker reports its state with OSC 7501 (Program Status Protocol) on its
// own terminal. tmux consumes OSC sequences it does not know, so capture-pane
// never shows them; the only place they survive is the pane's raw output.
// A paneTap reads that output through `tmux pipe-pane -O` into a FIFO and
// keeps only the latest status. It is bounded (one fixed read buffer, at most
// tapCarryMax bytes held for a sequence split across reads, a short message)
// and silent when idle: it blocks on the FIFO and has no timer, so a quiet
// pane costs nothing and a status that does not change wakes nobody.

const (
	// tapReadSize is the one read buffer a tap owns.
	tapReadSize = 16 << 10
	// tapCarryMax is the longest unterminated OSC 7501 sequence kept while
	// its terminator has not arrived; a longer one is dropped as malformed.
	tapCarryMax = 4 << 10
	// tapMsgMax bounds the status message, in runes, before redaction.
	tapMsgMax = 160
)

var oscPrefix = []byte("\x1b]7501;")

// tapPathSafe is what may appear in the FIFO path: tmux expands formats in
// the pipe-pane command and /bin/sh parses it, so the path is refused rather
// than quoted when it holds anything else.
var tapPathSafe = regexp.MustCompile(`^[A-Za-z0-9/._-]+$`)

// paneStatus is one pane's latest reported state, already made safe to show.
type paneStatus struct {
	State string // working, idle, blocked, done, error
	Kind  string // permission, question, auth (when blocked)
	Msg   string // one line, redacted, bounded
}

// paneTap follows one pane's output.
type paneTap struct {
	pane    string
	dir     string
	fifo    *os.File
	changed func()

	mu     sync.Mutex
	status paneStatus
	seen   bool
	carry  []byte
	done   chan struct{}
	closed bool
}

// Seams for tests: the tmux commands a tap issues.
var (
	tapPipe = tmux.PipePaneOutput
	tapStop = tmux.StopPipePane
)

// startPaneTap opens the pane's output pipe. changed is called, from the
// tap's goroutine, each time the reported status differs from the last one.
func startPaneTap(ctx context.Context, pane string, changed func()) (*paneTap, error) {
	if pane == "" {
		return nil, errors.New("pane status tap needs a pane")
	}
	dir, err := os.MkdirTemp("", "mx-osc-")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "out")
	if !tapPathSafe.MatchString(path) {
		os.RemoveAll(dir)
		return nil, errors.New("pane status tap: temporary path is not shell-safe: " + path)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	// Read-write so the open neither blocks waiting for a writer nor sees
	// end of file when cat restarts; Close unblocks the read.
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	t := &paneTap{pane: pane, dir: dir, fifo: f, changed: changed, done: make(chan struct{})}
	if err := tapPipe(ctx, pane, "exec cat > "+path); err != nil {
		f.Close()
		os.RemoveAll(dir)
		return nil, err
	}
	go t.read()
	return t, nil
}

func (t *paneTap) read() {
	defer close(t.done)
	buf := make([]byte, tapReadSize)
	for {
		n, err := t.fifo.Read(buf)
		if n > 0 {
			t.consume(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// consume scans one read for OSC 7501 sequences, keeping the unterminated
// tail of a sequence for the next read.
func (t *paneTap) consume(chunk []byte) {
	t.mu.Lock()
	data := chunk
	if len(t.carry) > 0 {
		data = append(t.carry, chunk...)
	}
	var latest lanes.ProgramStatus
	found := false
	if bytes.Contains(data, oscPrefix) {
		latest, found = lanes.ParseOSC7501(string(data))
	}
	t.carry = carryOSC(t.carry[:0], data)
	var next paneStatus
	if found {
		next = safeStatus(latest)
	}
	changed := false
	switch {
	case !found:
	case next.State == "clear":
		changed = t.seen
		t.status, t.seen = paneStatus{}, false
	case !t.seen || next != t.status:
		changed = true
		t.status, t.seen = next, true
	}
	t.mu.Unlock()
	if changed && t.changed != nil {
		t.changed()
	}
}

// carryOSC returns what of data must wait for the next read: an OSC 7501
// sequence whose terminator has not arrived, or a tail that may be the
// start of one. It appends into dst so the carry reuses its buffer.
func carryOSC(dst, data []byte) []byte {
	if i := bytes.LastIndex(data, oscPrefix); i >= 0 {
		rest := data[i+len(oscPrefix):]
		if bytes.IndexByte(rest, '\x07') < 0 && !bytes.Contains(rest, []byte("\x1b\\")) {
			if len(data)-i > tapCarryMax {
				return dst
			}
			return append(dst, data[i:]...)
		}
	}
	// A prefix split across reads: keep the tail from its last ESC when
	// that tail could still become the prefix.
	from := max(0, len(data)-len(oscPrefix)+1)
	if j := bytes.LastIndexByte(data[from:], '\x1b'); j >= 0 {
		tail := data[from+j:]
		if bytes.HasPrefix(oscPrefix, tail) {
			return append(dst, tail...)
		}
	}
	return dst
}

// safeStatus makes a reported status fit to show: known words only, the
// message one line, without control characters, bounded and redacted.
func safeStatus(ps lanes.ProgramStatus) paneStatus {
	word := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		if len(s) > 16 || strings.IndexFunc(s, func(r rune) bool { return r < 'a' || r > 'z' }) >= 0 {
			return ""
		}
		return s
	}
	msg := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, ps.Msg)
	msg = strings.Join(strings.Fields(msg), " ")
	if r := []rune(msg); len(r) > tapMsgMax {
		msg = string(r[:tapMsgMax-1]) + "…"
	}
	return paneStatus{State: word(ps.State), Kind: word(ps.Kind), Msg: RedactContent(msg, nil)}
}

// Status is the latest status the pane reported, and whether it reported one.
func (t *paneTap) Status() (paneStatus, bool) {
	if t == nil {
		return paneStatus{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status, t.seen
}

// stop closes the pipe and the FIFO and waits for the reader to end.
func (t *paneTap) stop() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = tapStop(ctx, t.pane)
	cancel()
	t.fifo.Close()
	<-t.done
	os.RemoveAll(t.dir)
}

// laneLast is what a lane's last-action column shows for a reported status:
// the message, led by the state when the worker is not simply working.
func (s paneStatus) laneLast() string {
	head := s.State
	if head == "blocked" && s.Kind != "" {
		head += " (" + s.Kind + ")"
	}
	switch {
	case head == "" || head == "working":
		return s.Msg
	case s.Msg == "":
		return head
	default:
		return head + " · " + s.Msg
	}
}

// tapPane starts following pane for the workspace's Board, replacing any
// earlier tap on it. The returned stop ends it.
func (w *Workspace) tapPane(ctx context.Context, pane string) func() {
	t, err := startPaneTap(ctx, pane, w.laneMoved)
	if err != nil {
		return func() {}
	}
	w.tapMu.Lock()
	if w.paneTaps == nil {
		w.paneTaps = map[string]*paneTap{}
	}
	old := w.paneTaps[pane]
	w.paneTaps[pane] = t
	w.tapMu.Unlock()
	if old != nil {
		go old.stopQuiet()
	}
	return func() {
		w.tapMu.Lock()
		if w.paneTaps[pane] == t {
			delete(w.paneTaps, pane)
		}
		w.tapMu.Unlock()
		t.stop()
		w.laneMoved()
	}
}

// stopQuiet ends a tap another one replaced without closing the pane's
// pipe, which now belongs to its successor.
func (t *paneTap) stopQuiet() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	t.mu.Unlock()
	t.fifo.Close()
	<-t.done
	os.RemoveAll(t.dir)
}

// paneStatusOf is the latest status the pane reported to its tap.
func (w *Workspace) paneStatusOf(pane string) (paneStatus, bool) {
	w.tapMu.Lock()
	t := w.paneTaps[pane]
	w.tapMu.Unlock()
	return t.Status()
}

// laneMoved tells the Board follower a lane's reported status changed.
// It never blocks: one pending nudge covers any number of changes.
func (w *Workspace) laneMoved() {
	select {
	case w.laneNudge <- struct{}{}:
	default:
	}
}
