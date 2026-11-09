package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestTaskDiffSources(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.test", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.test")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init")
	os.WriteFile(filepath.Join(repo, "a"), []byte("old\n"), 0600)
	git("add", "a")
	git("commit", "-m", "base")
	base := git("rev-parse", "HEAD")
	os.WriteFile(filepath.Join(repo, "a"), []byte("new\n"), 0600)
	git("commit", "-am", "result")
	result := git("rev-parse", "HEAD")
	raw := loadTaskDiff(context.Background(), repo, base, result)
	if !strings.Contains(raw, "+new") || !strings.Contains(raw, "-old") {
		t.Fatal(raw)
	}
	// A dirty checkout must not replace the recorded result.
	os.WriteFile(filepath.Join(repo, "a"), []byte("dirty\n"), 0600)
	if got := loadTaskDiff(context.Background(), repo, base, result); got != raw {
		t.Fatal(got)
	}
	for _, tc := range []struct{ base, result, want string }{{base, "", "worktree lookup unavailable"}, {"bogus", result, "validate commit"}, {base, "bogus", "validate commit"}, {"--help", result, "invalid commit"}} {
		if got := loadTaskDiff(context.Background(), repo, tc.base, tc.result); !strings.Contains(got, tc.want) {
			t.Fatal(got)
		}
	}
}

func TestTaskDiffTruncation(t *testing.T) {
	first := "diff --git a/a b/a\n@@ -1 +1 @@\n-a\n+b\n"
	raw := first + "diff --git a/b b/b\n" + strings.Repeat("x", taskDiffLimit)
	got := truncateTaskDiff([]byte(raw), true)
	if got != strings.TrimSuffix(first, "\n")+"\n… truncated\n" {
		t.Fatalf("unexpected boundary %q", got)
	}
	if got := truncateTaskDiff([]byte(first), false); got != first {
		t.Fatal(got)
	}
	b := &taskDiffBuffer{}
	b.Write([]byte(raw))
	if b.buf.Len() > taskDiffLimit+1 || !b.overflow {
		t.Fatal("unbounded output")
	}
}

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

func TestTaskDiffPending(t *testing.T) {
	dv := NewDiffViewer(nil, "")
	dv.OpenRaw("task t", "")
	ch := make(chan string, 1)
	ch <- "diff --git a/a b/a\n@@ -1 +1 @@\n-a\n+b\n"
	o := taskDiffOverlay{id: "t", viewer: dv, pending: ch}
	o.StatusLine(100)
	if len(dv.files) != 1 || o.pending != nil {
		t.Fatal("result not loaded")
	}
	if (&Workspace{}).openTaskDiff("missing") {
		t.Fatal("opened without base")
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
