package marshalsteps

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Appended lines reach a tail in order, only once each; a partial line
// waits until it is whole.
func TestAppendAndTail(t *testing.T) {
	path := Path(t.TempDir())
	tail := NewTail(path)
	if lines, reset, err := tail.Read(); err != nil || reset || len(lines) != 0 {
		t.Fatalf("missing log: %v %v %v", lines, reset, err)
	}
	at := time.Date(2026, 11, 20, 18, 41, 0, 0, time.UTC)
	if err := Append(path, Line{At: at, Kind: "start", Text: "session on claude", Provider: "claude"}); err != nil {
		t.Fatal(err)
	}
	if err := Append(path, Line{At: at.Add(time.Minute), Kind: "read", Text: "architecture context\npage 1/2"}); err != nil {
		t.Fatal(err)
	}
	lines, reset, err := tail.Read()
	if err != nil || reset || len(lines) != 2 || lines[1].Text != "architecture context page 1/2" || lines[0].Provider != "claude" || !lines[1].At.Equal(at.Add(time.Minute)) {
		t.Fatalf("first read: %+v %v %v", lines, reset, err)
	}
	if lines, _, _ := tail.Read(); len(lines) != 0 {
		t.Fatalf("read again: %+v", lines)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"at":"2026-11-20T18:43:00Z","kind":"read"`)
	if lines, _, _ := tail.Read(); len(lines) != 0 {
		t.Fatalf("partial line read: %+v", lines)
	}
	f.WriteString(`,"text":"memory recall"}` + "\n")
	f.Close()
	if lines, _, _ := tail.Read(); len(lines) != 1 || lines[0].Text != "memory recall" {
		t.Fatalf("completed line: %+v", lines)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
}

// The log stays bounded: past MaxBytes it keeps its newest KeepLines
// lines, and a tail of the old file starts over on the new one.
func TestAppendCompacts(t *testing.T) {
	path := Path(t.TempDir())
	tail := NewTail(path)
	at := time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC)
	if err := Append(path, Line{At: at, Kind: "read", Text: "first"}); err != nil {
		t.Fatal(err)
	}
	if lines, _, _ := tail.Read(); len(lines) != 1 {
		t.Fatalf("first: %+v", lines)
	}
	text := strings.Repeat("x", 100)
	for i := 0; i < 1200; i++ {
		if err := Append(path, Line{At: at.Add(time.Duration(i) * time.Second), Kind: "read", Text: text + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > MaxBytes+maxLine {
		t.Fatalf("log not bounded: %v %v", info.Size(), err)
	}
	lines, reset, err := tail.Read()
	if err != nil || !reset || len(lines) == 0 || len(lines) > KeepLines+KeepLines {
		t.Fatalf("after compaction: %d lines reset %v %v", len(lines), reset, err)
	}
	if last := lines[len(lines)-1].Text; !strings.HasSuffix(last, "1199") {
		t.Fatalf("newest line lost: %q", last)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".steps-*"))
	if len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

// A step line is short and on one line, whatever it is given.
func TestClean(t *testing.T) {
	if got := Clean("  read\t\x1b[31m page \n 2 "); got != "read [31m page 2" {
		t.Fatalf("Clean = %q", got)
	}
	if got := Clean(strings.Repeat("y", 500)); len([]rune(got)) != maxText+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("long text kept %d runes", len([]rune(got)))
	}
}
