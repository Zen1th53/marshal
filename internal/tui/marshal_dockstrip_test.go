package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/board/vm"
)

// stripFull is the full dock strip.
var stripFull = dockStrip{provider: "claude", model: "opus-5.5", effort: "medium",
	known: true, bound: true, clear: true, inputKnown: true, stage: "tasks · 6m", answered: 3}

// TestDockStripTitleFull: unfitted, the chat's title says what the v2
// MARSHAL CHAT strip says, then the [m] switch hint.
func TestDockStripTitleFull(t *testing.T) {
	for _, tc := range []struct {
		unicode bool
		want    string
	}{
		{true, " MARSHAL CHAT  ✳ claude · opus-5.5 · medium  │ bound to run ● live  │ stage tasks · 6m    3 requests answered · input clear   [m] switch  F11 Board "},
		{false, " MARSHAL CHAT  cl claude . opus-5.5 . medium  | bound to run * live  | stage tasks . 6m    3 requests answered . input clear   [m] switch  F11 Board "},
	} {
		got, at := dockStripTitle(stripFull, tc.unicode, 0, false)
		if got != tc.want {
			t.Errorf("unicode %v:\n got %q\nwant %q", tc.unicode, got, tc.want)
		}
		if !strings.HasPrefix(string([]rune(got)[at:]), "[m] switch") {
			t.Errorf("[m] at %d in %q", at, got)
		}
	}
}

// TestDockStripTitleWidths is the chat's title row beside the Board at 132,
// 100 and 80 window columns: exactly the chat's width less the focus marker,
// one row, right-hand parts gone first, the glyph, the binding and [m] kept.
func TestDockStripTitleWidths(t *testing.T) {
	unbound := dockStrip{provider: "codex", model: "6.1-Sol", known: true}
	bare := dockStrip{provider: "claude"}
	for _, tc := range []struct {
		name string
		s    dockStrip
		cols int
		want string
	}{
		{"full 132", stripFull, 132, " MARSHAL CHAT  ✳ opus-5.5  ● live         [m] switch "},
		{"full 100", stripFull, 100, " MARSHAL CHAT  ✳  ● live  [m] switch "},
		{"full 80", stripFull, 80, " ✳ opus-5.5  ● live    [m] "},
		{"unbound 132", unbound, 132, " MARSHAL CHAT  ⬡ 6.1-Sol  ○ not bound     [m] switch "},
		{"unbound 80", unbound, 80, " ⬡ codex  ○ not bound  [m] "},
		{"bare 132", bare, 132, " MARSHAL CHAT  ✳ claude        [m] switch  F11 Board "},
		{"bare 100", bare, 100, " MARSHAL CHAT  ✳ claude   [m] switch "},
		{"bare 80", bare, 80, " MARSHAL CHAT  ✳       [m] "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chatW := dockWidth(tc.cols)
			got, at := dockStripTitle(tc.s, true, chatW, true)
			if got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
			if n := VisibleLen(got); n != chatW-dockMarkerW {
				t.Errorf("width %d, want %d", n, chatW-dockMarkerW)
			}
			if at < 0 || !strings.HasPrefix(string([]rune(got)[at:]), "[m]") {
				t.Errorf("[m] at %d in %q", at, got)
			}
			if strings.ContainsAny(got, "\n\r") {
				t.Errorf("title wraps: %q", got)
			}
		})
	}
	// Far too narrow: the glyph and [m] still lead, tmux cuts the rest.
	got, _ := dockStripTitle(stripFull, true, 12, false)
	if !strings.HasPrefix(got, " ✳") || !strings.Contains(got, "[m]") {
		t.Errorf("narrowest title %q", got)
	}
}

// TestDockStripTitleUnknownParts: a field the collectors have not filled is
// left out of the title, never shown as a dash or as "not bound".
func TestDockStripTitleUnknownParts(t *testing.T) {
	for _, s := range []dockStrip{{}, {provider: "claude"}, {provider: "agy", model: "gemini-3"}} {
		got, _ := dockStripTitle(s, true, 0, false)
		for _, bad := range []string{"—", "-", "not bound", "input", "stage", "answered", "bound to run"} {
			if strings.Contains(got, bad) && !strings.Contains(s.model, bad) {
				t.Errorf("%+v: title %q shows %q", s, got, bad)
			}
		}
		if !strings.Contains(got, "MARSHAL CHAT") || !strings.Contains(got, "[m] switch") {
			t.Errorf("%+v: title %q", s, got)
		}
	}
	if got, _ := dockStripTitle(dockStrip{}, true, 0, false); got != " MARSHAL CHAT  chat    [m] switch  F11 Board " {
		t.Errorf("no provider: %q", got)
	}
}

func TestDockStripOf(t *testing.T) {
	if s := dockStripOf(vm.Board{}); s.known || s.answered != 0 {
		t.Fatalf("empty frame: %+v", s)
	}
	b := vm.Board{
		Marshal: vm.Marshal{Provider: "claude", Model: "opus#5.5", Effort: "high"},
		Requests: []vm.Request{
			{Sent: time.Unix(1, 0), Answer: "allow"}, {Answer: "decline"}, {Answer: "pending"}, {Answer: "expired"},
		},
	}
	s := dockStripOf(b)
	if !s.known || s.bound || s.clear || s.answered != 2 || s.model != "opus5.5" || s.effort != "high" {
		t.Fatalf("requests only: %+v", s)
	}
	b.Chat = vm.Chat{Bound: true, InputClear: true, InputKnown: true, Answered: 7, Stage: "review · 2m"}
	if s := dockStripOf(b); !s.bound || !s.clear || !s.inputKnown || s.answered != 7 || s.stage != "review · 2m" {
		t.Fatalf("chat: %+v", s)
	}
	// An input box not looked at yet says nothing: not "input waiting".
	b.Chat.InputKnown, b.Chat.InputClear = false, false
	s = dockStripOf(b)
	if got, _ := dockStripTitle(s, true, 0, false); s.inputKnown || s.clear || strings.Contains(got, "input") {
		t.Fatalf("unknown input: %+v %q", s, got)
	}
	b.Chat.InputKnown, b.Chat.InputClear = true, true
	// The chat collector's "live" and "not bound" stages are the binding
	// mark's to say; the title does not repeat them.
	for _, stage := range []string{"live", "not bound"} {
		b.Chat.Stage = stage
		if s := dockStripOf(b); s.stage != "" {
			t.Fatalf("stage %q repeated: %+v", stage, s)
		}
	}
}

// TestDockTitleEdition: Board v2 titles the chat with the strip, filled from
// the chat's own provider and --model while the Marshal collector is empty.
func TestDockTitleEdition(t *testing.T) {
	chat := &activeTmuxAgent{provider: "claude", args: []string{"--model", "claude-opus-5-5"}}
	w := NewWorkspace(nil, "test-proj", "test-session")
	w.workDir = t.TempDir()
	if got, want := w.dockTitle(chat, 0), " paste/tmux MARSHAL CHAT  ✳ claude · Opus 5.5    [m] switch  F11 Board "; got != want {
		t.Errorf("v2:\n got %q\nwant %q", got, want)
	}
}

// TestNoteDockStripKicksOnChange: a painted frame updates the chat's title
// only when the chat is docked and the strip says something new.
func TestNoteDockStripKicksOnChange(t *testing.T) {
	w := &Workspace{}
	w.dock.kick = make(chan struct{}, 1)
	b := vm.Board{Marshal: vm.Marshal{Provider: "codex"}}
	w.noteDockStrip(b)
	if w.dock.strip.Load() != nil {
		t.Fatal("undocked paint recorded a strip")
	}
	w.dock.docked.Store(true)
	wait := func() bool {
		select {
		case <-w.dock.kick:
			return true
		case <-time.After(2 * time.Second):
			return false
		}
	}
	w.noteDockStrip(b)
	if !wait() {
		t.Fatal("first docked paint did not kick the dock")
	}
	w.noteDockStrip(b)
	select {
	case <-w.dock.kick:
		t.Fatal("an unchanged strip kicked the dock")
	case <-time.After(50 * time.Millisecond):
	}
	b.Chat.Stage = "tasks · 1m"
	w.noteDockStrip(b)
	if !wait() {
		t.Fatal("a new stage did not kick the dock")
	}
}
