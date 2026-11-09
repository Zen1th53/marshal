package tui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestAttentionEdges(t *testing.T) {
	now := time.Unix(100, 0)
	base := attnSnapshot{observed: true, runID: "R1"}
	cases := []struct {
		name       string
		prev, next attnSnapshot
		title      string
	}{
		{"first", attnSnapshot{}, attnSnapshot{runID: "R1", needsYou: true}, ""},
		{"new run", base, attnSnapshot{runID: "R2", needsYou: true, closed: true}, ""},
		{"attention", base, attnSnapshot{runID: "R1", needsYou: true}, "Marshal needs you"},
		{"closed", base, attnSnapshot{runID: "R1", closed: true}, "Run closed"},
		{"stopped", base, attnSnapshot{runID: "R1", stopped: true, stopNote: "failed"}, "Run stopped: failed"},
		{"closed overrides stopped", base, attnSnapshot{runID: "R1", closed: true, stopped: true}, "Run closed"},
		{"steady", base, base, ""},
		{"cleared", attnSnapshot{observed: true, runID: "R1", needsYou: true, stopped: true}, base, ""},
		{"same stop", attnSnapshot{observed: true, runID: "R1", stopped: true, stopNote: "failed"}, attnSnapshot{runID: "R1", stopped: true, stopNote: "failed"}, ""},
		{"changed stop", attnSnapshot{observed: true, runID: "R1", stopped: true, stopNote: "old"}, attnSnapshot{runID: "R1", stopped: true, stopNote: "new"}, "Run stopped: new"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			events := attentionEdges(c.prev, c.next, now)
			if c.title == "" {
				if len(events) != 0 {
					t.Fatal(events)
				}
				return
			}
			if len(events) != 1 || events[0].title != c.title || !strings.Contains(events[0].body, "R1") {
				t.Fatal(events)
			}
		})
	}
	for _, kind := range []string{"attention", "stopped", "closed"} {
		prev := base
		prev.seen = map[string]time.Time{kind + ":R1": now}
		next := attnSnapshot{runID: "R1", needsYou: kind == "attention", stopped: kind == "stopped", closed: kind == "closed"}
		if got := len(attentionEdges(prev, next, now.Add(29*time.Second))); (got == 1) != (kind == "closed") {
			t.Fatalf("dedup %s: %d", kind, got)
		}
		if len(attentionEdges(prev, next, now.Add(30*time.Second))) != 1 {
			t.Fatal("edge not restored", kind)
		}
	}
}

func TestAttentionSnapshot(t *testing.T) {
	w, _ := boardWorkspace(t)
	p := newMarshalPanel("R1", "codex", fakeRun(), "")
	w.setMarshalPanel(p)
	// A live inbox request is not itself an owner gate or a waiting stop.
	if len(w.boardInbox()) == 0 {
		t.Fatal("fixture must have an inbox request")
	}
	if s := w.attentionNow(); s.needsYou || s.stopped || s.closed {
		t.Fatalf("inbox alone: %+v", s)
	}
	p.Board.Run.State = marshal.AwaitingUser
	p.Board.Run.Paused = "decision"
	if s := w.attentionNow(); !s.needsYou || s.stopped {
		t.Fatalf("waiting: %+v", s)
	}
	p.Board.Run.State = marshal.Dispatching
	p.Note = "stopped: worker failed"
	if s := w.attentionNow(); s.needsYou || !s.stopped || s.stopNote != "worker failed" {
		t.Fatalf("stop: %+v", s)
	}
	p.Board.Run.State = marshal.Closed
	if s := w.attentionNow(); !s.closed || s.stopped {
		t.Fatalf("closed: %+v", s)
	}
	p.Note = ""
	p.Board.Run.State = marshal.Drafting
	p.Board.Run.Total = 1
	if s := w.attentionNow(); !s.needsYou {
		t.Fatalf("owner gate: %+v", s)
	}
}

func TestAttentionSettings(t *testing.T) {
	root := t.TempDir()
	path := attentionPath(root)
	if !attentionEnabled(root) {
		t.Fatal("missing should enable")
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("read created directory")
	}
	for _, value := range []string{"off", "on", "anything"} {
		if err := saveAttention(root, value); err != nil {
			t.Fatal(err)
		}
		if attentionEnabled(root) != (value != "off") {
			t.Fatal(value)
		}
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0o600 {
			t.Fatal(info.Mode())
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if !attentionEnabled(root) {
		t.Fatal("unreadable file should enable")
	}
	if saveAttention("", "off") == nil {
		t.Fatal("empty root accepted")
	}
}

type attentionRecorder struct {
	bells    int
	messages chan [2]string
}

func (n *attentionRecorder) Bell() error { n.bells++; return nil }
func (n *attentionRecorder) Notify(_ context.Context, title, body string) error {
	n.messages <- [2]string{title, body}
	return nil
}
func TestAttentionDelivery(t *testing.T) {
	w, _ := boardWorkspace(t)
	p := newMarshalPanel("R1", "codex", fakeRun(), "")
	w.setMarshalPanel(p)
	n := &attentionRecorder{messages: make(chan [2]string, 10)}
	now := time.Unix(100, 0)
	w.attn.notifier = n
	w.attn.now = func() time.Time { return now }
	ctx := context.Background()
	w.attentionPoll(ctx)
	p.Board.Run.State = marshal.AwaitingUser
	w.attentionPoll(ctx)
	select {
	case m := <-n.messages:
		if m[0] != "Marshal needs you" || m[1] != "Run R1 needs your attention on the Board." {
			t.Fatal(m)
		}
	case <-time.After(time.Second):
		t.Fatal("no notification")
	}
	if n.bells != 1 {
		t.Fatal(n.bells)
	}
	p.Board.Run.State = marshal.Dispatching
	w.attentionPoll(ctx)
	p.Board.Run.State = marshal.AwaitingUser
	w.attentionPoll(ctx)
	if n.bells != 1 {
		t.Fatal("dedup did not use fake clock")
	}
	now = now.Add(30 * time.Second)
	p.Board.Run.State = marshal.Dispatching
	w.attentionPoll(ctx)
	p.Board.Run.State = marshal.AwaitingUser
	w.attentionPoll(ctx)
	if n.bells != 2 {
		t.Fatal("dedup did not expire")
	}
	select {
	case <-n.messages:
	case <-time.After(time.Second):
		t.Fatal("no second notification")
	}
	w.notifyCommand([]string{"off"})
	p.Board.Run.State = marshal.Closed
	w.attentionPoll(ctx)
	if n.bells != 2 {
		t.Fatal("off rang")
	}
	select {
	case m := <-n.messages:
		t.Fatalf("off delivered: %v", m)
	default:
	}
	w.notifyCommand([]string{"status"})
	notes := w.noteList()
	if len(notes) == 0 || !strings.Contains(notes[0].Text, "off for this project (") || !strings.Contains(notes[0].Text, attentionPath(w.providerRoot())) {
		t.Fatal(notes)
	}
	w.notifyCommand([]string{"on"})
	if !attentionEnabled(w.providerRoot()) {
		t.Fatal("on failed")
	}
}

func TestAttentionDesktop(t *testing.T) {
	var out bytes.Buffer
	n := &desktopNotifier{terminal: NewTerminal(strings.NewReader(""), &out)}
	if err := n.Bell(); err != nil || out.String() != "\a" {
		t.Fatalf("bell %q %v", out.String(), err)
	}
	t.Setenv("PATH", t.TempDir())
	if err := n.Notify(context.Background(), "title", "body"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	capture := filepath.Join(dir, "args")
	t.Setenv("ATTENTION_CAPTURE", capture)
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ATTENTION_CAPTURE\"\n"
	if err := os.WriteFile(filepath.Join(dir, "notify-send"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if err := n.Notify(context.Background(), "cached absent", "body"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(capture); !os.IsNotExist(err) {
		t.Fatal("lookup not cached")
	}
	n = &desktopNotifier{}
	if err := n.Notify(context.Background(), "title; literal", "body $literal"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture)
	if err != nil || string(data) != "-a\nMARSHAL\ntitle; literal\nbody $literal\n" {
		t.Fatalf("args %q %v", data, err)
	}
}
