package tui

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestTaskDiffKeys(t *testing.T) {
	for _, mode := range []marshal.AcceptanceMode{marshal.AcceptUser, marshal.AcceptMarshalThenUser, marshal.AcceptMarshal} {
		for _, handed := range []bool{false, true} {
			t.Run(string(mode)+map[bool]string{false: "/working", true: "/handed"}[handed], func(t *testing.T) {
				dv := NewDiffViewer(nil, "")
				dv.OpenRaw("task t", "diff --git a/a b/a\n@@ -1 +1 @@\n-a\n+b\n@@ -3 +3 @@\n-c\n+d\n")
				accepts, returns := 0, 0
				msg := ""
				o := taskDiffOverlay{id: "t", mode: mode, handed: handed, viewer: dv, accept: func() (string, error) { accepts++; return "runtime approval", nil }, rework: func(reason string) (string, error) {
					returns++
					if reason != "fix" {
						t.Fatal(reason)
					}
					return "", errors.New("runtime refused")
				}, say: func(s string) string { msg = s; return "your input was sent" }}
				key := func(r rune) { o.HandleKey(nil, KeyEvent{Type: KeyRune, Rune: r}) }
				enabled := handed || mode == marshal.AcceptMarshal
				key('a')
				if !enabled {
					if !strings.Contains(o.StatusLine(200), "disabled") {
						t.Fatal(o.StatusLine(200))
					}
					return
				}
				if mode != marshal.AcceptMarshal {
					key('n')
					o.HandleKey(nil, KeyEvent{Type: KeyEnter})
					if accepts != 0 || dv.currentHunk != 1 {
						t.Fatal("disarm/navigation")
					}
					key('a')
					o.HandleKey(nil, KeyEvent{Type: KeyEnter})
					if accepts != 1 {
						t.Fatal("accept")
					}
				} else if msg != "Owner review of task t: accept" {
					t.Fatal(msg)
				}
				key('r')
				o.HandleKey(nil, KeyEvent{Type: KeyEnter})
				if !o.prompt {
					t.Fatal("empty reason allowed")
				}
				key('x')
				o.HandleKey(nil, KeyEvent{Type: KeyEsc})
				if o.prompt {
					t.Fatal("cancel")
				}
				key('r')
				for _, r := range "fix" {
					key(r)
				}
				o.HandleKey(nil, KeyEvent{Type: KeyEnter})
				if mode == marshal.AcceptMarshal {
					if msg != "Owner review of task t: rework — fix" {
						t.Fatal(msg)
					}
				} else if returns != 1 || !strings.Contains(o.StatusLine(200), "runtime refused") {
					t.Fatal(o.StatusLine(200))
				}
				key('q')
				if dv.IsOpen() || o.StatusLine(80) != "" {
					t.Fatal("close")
				}
			})
		}
	}
}

func TestTaskDiffStatusGolden(t *testing.T) {
	dv := NewDiffViewer(nil, "")
	dv.OpenRaw("task t", "")
	o := taskDiffOverlay{id: "t", handed: true, mode: marshal.AcceptUser, viewer: dv}
	lines := []string{o.StatusLine(200)}
	o.armed = true
	lines = append(lines, o.StatusLine(200))
	o.armed = false
	o.prompt = true
	o.reason = "fix"
	lines = append(lines, o.StatusLine(200))
	o.prompt = false
	o.handed = false
	lines = append(lines, o.StatusLine(200))
	o.mode = marshal.AcceptMarshal
	o.reply = "decided by Marshal — your input was sent"
	lines = append(lines, o.StatusLine(200))
	want, err := os.ReadFile("testdata/board/task-diff/status.golden")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(lines, "\n") + "\n"; got != string(want) {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
