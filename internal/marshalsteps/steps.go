// Package marshalsteps is the Marshal's step log: one short plain line per
// MARSHAL tool call the live Marshal makes, appended where the call is
// served (the proposal broker in the Board process, the Marshal's own MCP
// child for the tools it answers itself) and tailed by the Board's steps
// pane. A line says which kind of call it was and a few words about it;
// it never carries prompt text or what was submitted.
//
// The file lives in the state directory, so the Board shows the steps of a
// Marshal it did not serve itself (an earlier Board process, the MCP
// child) and keeps them across a Board restart. It is bounded: when it
// outgrows MaxBytes the writer rewrites it to its newest KeepLines lines.
// It is a view of recent activity, not a record: two writers compacting at
// the same moment can drop a line.
package marshalsteps

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// Dir and FileName place the log under the state directory: .marshal/marshal
// is a directory the Marshal chat may write (its confinement keeps it
// writable), so its MCP child can append there.
const (
	Dir      = "marshal"
	FileName = "steps.jsonl"
)

// Bounds of the log and of one line.
const (
	MaxBytes  = 64 << 10
	KeepLines = 200
	maxText   = 120
	maxLine   = 512
)

// Line is one step. Kind is "" for a call that only moves the quiet clock
// (a polled receipt); "start" opens a Marshal session on Provider.
type Line struct {
	At       time.Time `json:"at"`
	Kind     string    `json:"kind,omitempty"`
	Text     string    `json:"text,omitempty"`
	Provider string    `json:"provider,omitempty"`
}

// Path is the log's path in the state directory stateDir.
func Path(stateDir string) string {
	return filepath.Join(stateDir, Dir, FileName)
}

// Clean keeps s short, printable and on one line for a step.
func Clean(s string) string {
	var b strings.Builder
	n := 0
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) || !unicode.IsPrint(r) {
			space = b.Len() > 0
			continue
		}
		if n >= maxText {
			b.WriteString("…")
			break
		}
		if space {
			b.WriteByte(' ')
			n++
			space = false
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// Append adds l to the log at path, creating it (and its directory) when
// missing, and compacts the log once it outgrows MaxBytes.
func Append(path string, l Line) error {
	l.Kind, l.Text, l.Provider = Clean(l.Kind), Clean(l.Text), Clean(l.Provider)
	if l.At.IsZero() {
		l.At = time.Now()
	}
	l.At = l.At.UTC()
	raw, err := json.Marshal(l)
	if err != nil {
		return err
	}
	if len(raw) >= maxLine {
		return errors.New("marshalsteps: line too long")
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	// One write of a short line: appends from two processes do not interleave.
	_, werr := f.Write(raw)
	info, serr := f.Stat()
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	if cerr != nil {
		return cerr
	}
	if serr == nil && info.Size() > MaxBytes {
		// The line is already in the log; a failed compaction only leaves
		// it longer, and must not read as a lost line (the caller would
		// keep it a second time). The next append tries again.
		_ = compact(path)
	}
	return nil
}

// compact rewrites the log to its newest KeepLines lines.
func compact(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	if n := len(lines); n > 0 && len(lines[n-1]) == 0 {
		lines = lines[:n-1]
	}
	if len(lines) > KeepLines {
		lines = lines[len(lines)-KeepLines:]
	}
	// Append never writes a line this long: one that is came from elsewhere
	// and is dropped, so it is not read again at every compaction.
	kept := lines[:0]
	for _, l := range lines {
		if len(l) <= maxLine {
			kept = append(kept, l)
		}
	}
	lines = kept
	tmp, err := os.CreateTemp(filepath.Dir(path), ".steps-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(bytes.Join(lines, nil)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Tail reads the lines added to a log since it last looked.
type Tail struct {
	path string
	info os.FileInfo // the file last read, nil before the first read
	off  int64       // bytes of it consumed (whole lines only)
}

// NewTail tails the log at path.
func NewTail(path string) *Tail { return &Tail{path: path} }

// Read returns the lines added since the last Read. reset is true when the
// log was replaced or truncated (compacted, removed and made again): lines
// is then the whole log and the caller drops what it kept. A log that does
// not exist reads as no lines.
func (t *Tail) Read() (lines []Line, reset bool, err error) {
	info, err := os.Stat(t.path)
	if errors.Is(err, os.ErrNotExist) {
		reset = t.info != nil
		t.info, t.off = nil, 0
		return nil, reset, nil
	}
	if err != nil {
		return nil, false, err
	}
	if t.info != nil && os.SameFile(t.info, info) && info.Size() == t.off && info.ModTime().Equal(t.info.ModTime()) {
		return nil, false, nil
	}
	if t.info == nil || !os.SameFile(t.info, info) || info.Size() < t.off {
		reset = t.info != nil
		t.off = 0
	}
	f, err := os.Open(t.path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	// Re-stat the opened file: a compaction may have replaced the path.
	if opened, err := f.Stat(); err == nil {
		if !os.SameFile(opened, info) {
			info = opened
			reset, t.off = t.info != nil, 0
		}
	}
	start := t.off
	if start == 0 && info.Size() > 2*MaxBytes {
		// Never read more than a bounded log's worth; skip to a line start.
		start = info.Size() - 2*MaxBytes
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, false, err
	}
	r := bufio.NewReader(io.LimitReader(f, 4*MaxBytes))
	consumed := start
	first := start > t.off // a cut into the middle of a line: drop it
	for {
		raw, err := r.ReadBytes('\n')
		if err != nil {
			// A partial last line is read again once it is whole.
			break
		}
		consumed += int64(len(raw))
		if first {
			first = false
			continue
		}
		var l Line
		if json.Unmarshal(raw, &l) == nil && !l.At.IsZero() {
			lines = append(lines, l)
		}
	}
	t.info, t.off = info, consumed
	return lines, reset, nil
}
