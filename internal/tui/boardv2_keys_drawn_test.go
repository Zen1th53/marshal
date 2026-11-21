//go:build linux

package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// Every key the Board v2 draws acts as labelled in the state it is drawn
// in, or is not drawn: pressing it never starts a message to the Marshal.

// drawnCols and drawnRows are the size the Board is painted at; the walk
// also paints the smaller sizes, where panes give way.
var drawnCols, drawnRows = 170, 45

// drawnHints are the key hints the v2 renderer writes into pane titles and
// card bodies (the key bar and the buttons are click regions, read from the
// hits), with the key each names.
var drawnHints = []struct{ text, key string }{
	{"m switch", "m"},
	{"v re-check", "v"},
	{"i opens", "i"},
	{"⏎ open ", "⏎"},
	{"r restart", "r"},
	{"esc later", "esc"},
	{"faster model", "f"},
	{"y keep on", "y"},
	{"y turn on", "y"},
	{"t turn off", "t"},
	{"t keep off", "t"},
	{"? what is sent", "?"},
	{"g opens it here", "g"},
	{"accept here", "⏎"},
	{"pause all", "p"},
	{"l  limits", "l"},
}

// drawnKeys paints w and returns the hint bar's actions, the keys of the
// buttons and card key bar, the keys the text names, and the frame.
func drawnKeys(t *testing.T, w *Workspace) (ids, keys []string, text string) {
	t.Helper()
	lines, _, _ := w.frameLines(drawnCols, drawnRows)
	text = StripANSI(strings.Join(lines, "\n"))
	w.boardHits.mu.Lock()
	hits := slices.Clone(w.boardHits.hits)
	w.boardHits.mu.Unlock()
	for _, h := range hits {
		switch h.kind {
		case hitAction:
			if !slices.Contains(ids, h.id) {
				ids = append(ids, h.id)
			}
		case hitV2Key:
			if !slices.Contains(keys, h.id) {
				keys = append(keys, h.id)
			}
		}
	}
	for _, d := range drawnHints {
		if strings.Contains(text, d.text) && !slices.Contains(keys, d.key) {
			keys = append(keys, d.key)
		}
	}
	return ids, keys, text
}

// pressDrawn presses key on a Board just painted and reports whether the
// Board acted on it: Enter and Esc must be taken by the Board's handlers, a
// letter must not end up typed as a message to the Marshal.
func pressDrawn(t *testing.T, w *Workspace, key string) bool {
	t.Helper()
	w.frameLines(drawnCols, drawnRows)
	ev, ok := v2KeyEvent(key)
	if !ok {
		t.Fatalf("drawn key %q is no key", key)
	}
	if ev.Type == KeyEnter || ev.Type == KeyEsc {
		return w.handleBoardKey(ev)
	}
	w.handleTerminalKey(context.Background(), ev)
	return !strings.Contains(w.composer.Text(), string(ev.Rune))
}

// drawnSeams replaces every effect a drawn key can reach in these states.
func drawnSeams(t *testing.T) (signIn *[]string) {
	t.Helper()
	displaySeams(t, "linux", nil)
	recordStopCommands(t)
	origClip := v2ClipboardWrite
	t.Cleanup(func() { v2ClipboardWrite = origClip })
	v2ClipboardWrite = func(*Workspace, string) bool { return true }
	origTel := v2TelemetryParts
	t.Cleanup(func() { v2TelemetryParts = origTel })
	v2TelemetryParts.keep = func(context.Context, string) error { return nil }
	v2TelemetryParts.disable = func(context.Context, string) error { return nil }
	var mu sync.Mutex
	var opened []string
	origSign := v2SignInParts
	t.Cleanup(func() { v2SignInParts = origSign })
	v2SignInParts.here = func(*Workspace) bool { return true }
	v2SignInParts.open = func(_ context.Context, _ *Workspace, dir string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		opened = append(opened, dir)
		return "%99", nil
	}
	v2SignInParts.closed = func(context.Context, *Workspace, string) bool { return false }
	return &opened
}

// TestV2DrawnKeysAct walks each Board v2 screen the live Board draws: the
// readiness screen (with the Usage counts card and agy not signed in),
// planning, a run, the architecture and inbox decision cards and the paused
// card. Each hint bar action can run now, and each other key drawn, pressed
// on that screen, is acted on.
func TestV2DrawnKeysAct(t *testing.T) {
	for _, size := range [][2]int{{170, 45}, {100, 30}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			drawnCols, drawnRows = size[0], size[1]
			t.Cleanup(func() { drawnCols, drawnRows = 170, 45 })
			walkDrawnKeys(t)
		})
	}
}

func walkDrawnKeys(t *testing.T) {
	for _, sc := range []struct {
		name   string
		screen vm.Screen
		setup  func(t *testing.T) *Workspace
	}{
		{"ready", vm.ScreenReady, func(t *testing.T) *Workspace {
			w := emptyBoardWorkspace(t)
			w.endOpening()
			return w
		}},
		{"ready with cards", vm.ScreenReady, func(t *testing.T) *Workspace {
			w := v2Fixture(t, time.Now())
			w.endOpening()
			return w
		}},
		{"planning", vm.ScreenPlanning, func(t *testing.T) *Workspace {
			w := emptyBoardWorkspace(t)
			w.endOpening()
			w.planning.Store(true)
			return w
		}},
		{"running", vm.ScreenRunning, func(t *testing.T) *Workspace { return runHintWorkspace(t, "T-017") }},
		{"decision architecture", vm.ScreenDecision, func(t *testing.T) *Workspace {
			w := architectureWorkspace(t)
			w.endOpening()
			waitCards(t, w, "the architecture card", sketched)
			return w
		}},
		{"decision inbox", vm.ScreenDecision, func(t *testing.T) *Workspace {
			w, _ := boardWorkspace(t)
			t.Cleanup(func() { v2Cards.Delete(w) })
			return w
		}},
		{"paused", vm.ScreenPaused, func(t *testing.T) *Workspace {
			return pausedWorkspace(t, "codex", marshal.Pause{Reason: "worker failed", Detail: "codex: exit 1", Code: marshal.CodeWorkerAuth, Action: marshal.PauseResume})
		}},
	} {
		t.Run(sc.name, func(t *testing.T) {
			drawnSeams(t)
			w := sc.setup(t)
			defer w.Close()
			ids, keys, text := drawnKeys(t, w)
			if got := w.v2CardState().shownScreen(); got != sc.screen {
				t.Fatalf("screen %q, want %q:\n%s", got, sc.screen, text)
			}
			if len(ids)+len(keys) == 0 {
				t.Fatalf("no keys drawn:\n%s", text)
			}
			for _, id := range ids {
				k, ok := boardAction(id)
				if !ok {
					t.Errorf("key bar action %q is not in the keymap", id)
					continue
				}
				if run, why := k.available(w); !run {
					t.Errorf("key bar shows %q, which cannot run now: %s", id, why)
				}
			}
			for _, key := range keys {
				fresh := sc.setup(t)
				if !pressDrawn(t, fresh, key) {
					t.Errorf("%q is drawn but the Board does not act on it (composer %q, output %q):\n%s", key, fresh.composer.Text(), lastOutput(fresh), text)
				}
				fresh.Close()
			}
		})
	}
}

// shownScreen is the screen the last v2 paint drew.
func (s *v2CardState) shownScreen() vm.Screen {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shown
}

// A card's key bar is its own buttons (esc for "later" on a decision) and
// the palette and help, never the screen under it: no p, l or f.
func TestV2CardKeyBarIsTheCards(t *testing.T) {
	drawnSeams(t)
	w := pausedWorkspace(t, "codex", marshal.Pause{Reason: "worker failed", Detail: "codex: exit 1", Code: marshal.CodeWorkerAuth, Action: marshal.PauseResume})
	defer w.Close()
	lines, _, _ := w.frameLines(drawnCols, drawnRows)
	bar := strings.Join(strings.Fields(StripANSI(lines[drawnRows-2])), " ")
	b := cardsFrame(w)
	if b.Paused == nil {
		t.Fatal("no paused card")
	}
	for _, a := range b.Paused.Actions {
		if !strings.Contains(bar, a.Key+" "+a.Label) {
			t.Errorf("key bar %q lacks the card's %q", bar, a.Key+" "+a.Label)
		}
	}
	for _, want := range []string{"space actions", "? help"} {
		if !strings.Contains(bar, want) {
			t.Errorf("key bar %q lacks %q", bar, want)
		}
	}
	for _, dead := range []string{"pause all", "limits", "faster model"} {
		if strings.Contains(bar, dead) {
			t.Errorf("key bar %q shows %q", bar, dead)
		}
	}

	a := architectureWorkspace(t)
	defer a.Close()
	waitCards(t, a, "the architecture card", sketched)
	lines, _, _ = a.frameLines(drawnCols, drawnRows)
	text := StripANSI(strings.Join(lines, "\n"))
	if !boardAcceptsArchitecture && strings.Contains(text, "accept here") {
		t.Fatalf("the Board offers to accept the architecture:\n%s", text)
	}
	if bar := strings.Join(strings.Fields(StripANSI(lines[drawnRows-2])), " "); !strings.Contains(bar, "esc later") {
		t.Fatalf("decision key bar %q", bar)
	}
}

// The planning screen offers no faster model: no path switches the
// Marshal's model for the rest of a step.
func TestV2PlanningOffersNoFasterModel(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.endOpening()
	w.planning.Store(true)
	lines, _, _ := w.frameLines(drawnCols, drawnRows)
	if text := StripANSI(strings.Join(lines, "\n")); strings.Contains(text, "faster model") || !strings.Contains(text, "MARSHAL STEPS") {
		t.Fatalf("planning screen:\n%s", text)
	}
}

// The Usage counts card: ? says what is sent and the key bar says so; y
// keeps telemetry on and records the notice; t turns it off as marshal
// telemetry disable does. After either the card goes.
func TestV2UsageCountsCardKeys(t *testing.T) {
	for _, key := range []rune{'y', 't'} {
		t.Run(string(key), func(t *testing.T) {
			drawnSeams(t)
			var mu sync.Mutex
			var kept, disabled []string
			v2TelemetryParts.keep = func(_ context.Context, root string) error {
				mu.Lock()
				defer mu.Unlock()
				kept = append(kept, root)
				return nil
			}
			v2TelemetryParts.disable = func(_ context.Context, root string) error {
				mu.Lock()
				defer mu.Unlock()
				disabled = append(disabled, root)
				return nil
			}
			w := v2Fixture(t, time.Now())
			defer w.Close()
			w.endOpening()
			lines, _, _ := w.frameLines(drawnCols, drawnRows)
			text := StripANSI(strings.Join(lines, "\n"))
			if !strings.Contains(text, "USAGE COUNTS") || !strings.Contains(StripANSI(lines[drawnRows-2]), "?  what is sent") {
				t.Fatalf("no Usage counts card or ? label:\n%s", text)
			}
			w.handleTerminalKey(context.Background(), KeyEvent{Type: KeyRune, Rune: '?'})
			if !strings.Contains(lastOutput(w), "random installation ID") || w.boardHelp.Load() {
				t.Fatalf("? did not say what is sent: %q", lastOutput(w))
			}
			w.frameLines(drawnCols, drawnRows) // the keys act on what the last paint drew
			w.handleTerminalKey(context.Background(), KeyEvent{Type: KeyRune, Rune: key})
			deadline := time.Now().Add(5 * time.Second)
			for {
				mu.Lock()
				n := len(kept) + len(disabled)
				mu.Unlock()
				if n > 0 && strings.Contains(lastOutput(w), "Usage counts") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("%c saved nothing: %q", key, lastOutput(w))
				}
				time.Sleep(10 * time.Millisecond)
			}
			mu.Lock()
			if key == 'y' && (len(kept) != 1 || len(disabled) != 0) || key == 't' && (len(disabled) != 1 || len(kept) != 0) {
				t.Fatalf("kept %v disabled %v", kept, disabled)
			}
			mu.Unlock()
			if w.composer.Text() != "" {
				t.Fatalf("%c typed %q", key, w.composer.Text())
			}
			lines, _, _ = w.frameLines(drawnCols, drawnRows)
			if text := StripANSI(strings.Join(lines, "\n")); strings.Contains(text, "USAGE COUNTS") {
				t.Fatalf("the card stayed after %c:\n%s", key, text)
			}
			if key == 't' {
				w.v2m.mu.Lock()
				on := w.v2m.probe.TelemetryOn
				w.v2m.mu.Unlock()
				if on {
					t.Fatal("t left telemetry on")
				}
			}
		})
	}
}

// g is offered when agy is installed but not signed in and the Board runs
// in tmux: it opens agy in a window in the project, and when that window
// closes the verify of agy is armed. Outside tmux the plain hint stays.
func TestV2AgySignInKey(t *testing.T) {
	opened := drawnSeams(t)
	var mu sync.Mutex
	closed := false
	v2SignInParts.every = 5 * time.Millisecond
	v2SignInParts.closed = func(context.Context, *Workspace, string) bool {
		mu.Lock()
		defer mu.Unlock()
		return closed
	}
	w := v2Fixture(t, time.Now())
	defer w.Close()
	w.endOpening()
	keys, _ := v2CardButtons(t, w, drawnCols, drawnRows)
	if !slices.Contains(keys, "g") {
		t.Fatalf("no g button: %v", keys)
	}
	w.handleTerminalKey(context.Background(), KeyEvent{Type: KeyRune, Rune: 'g'})
	if len(*opened) != 1 || (*opened)[0] != w.providerRoot() || w.composer.Text() != "" {
		t.Fatalf("g opened %v, typed %q", *opened, w.composer.Text())
	}
	w.frameLines(drawnCols, drawnRows)
	w.handleTerminalKey(context.Background(), KeyEvent{Type: KeyRune, Rune: 'g'})
	if len(*opened) != 1 || !strings.Contains(lastOutput(w), "already open") {
		t.Fatalf("a second g opened %v: %q", *opened, lastOutput(w))
	}
	mu.Lock()
	closed = true
	mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for w.firstUseArmed() != "agy" {
		if time.Now().After(deadline) {
			t.Fatalf("closing the window did not arm agy's verify: %q", lastOutput(w))
		}
		time.Sleep(5 * time.Millisecond)
	}

	v2SignInParts.here = func(*Workspace) bool { return false }
	plain := v2Fixture(t, time.Now())
	defer plain.Close()
	plain.endOpening()
	if keys, frame := v2CardButtons(t, plain, drawnCols, drawnRows); slices.Contains(keys, "g") || strings.Contains(frame, "g opens it here") {
		t.Fatalf("g offered outside tmux: %v\n%s", keys, frame)
	}

	v2SignInParts.here = func(*Workspace) bool { return true }
	v2SignInParts.open = func(context.Context, *Workspace, string) (string, error) { return "", errors.New("no tmux server") }
	failing := v2Fixture(t, time.Now())
	defer failing.Close()
	failing.endOpening()
	failing.frameLines(drawnCols, drawnRows)
	failing.handleTerminalKey(context.Background(), KeyEvent{Type: KeyRune, Rune: 'g'})
	if !strings.Contains(lastOutput(failing), "run agy once") {
		t.Fatalf("a failed open did not say how to sign in: %q", lastOutput(failing))
	}
}

// A Usage counts card that did not fit takes no key: t is not a telemetry
// choice there and ? is help. A click on the key bar's "? what is sent"
// says what is sent where the card is drawn.
func TestV2UsageCountsKeysOnlyWhereDrawn(t *testing.T) {
	drawnSeams(t)
	var mu sync.Mutex
	calls := 0
	v2TelemetryParts.disable = func(context.Context, string) error { mu.Lock(); calls++; mu.Unlock(); return nil }
	v2TelemetryParts.keep = v2TelemetryParts.disable
	w := v2Fixture(t, time.Now())
	defer w.Close()
	w.endOpening()
	lines, _, _ := w.frameLines(80, 24)
	if text := StripANSI(strings.Join(lines, "\n")); strings.Contains(text, "t turn off") || strings.Contains(text, "what is sent") {
		t.Fatalf("the card or its label drawn at 80x24:\n%s", text)
	}
	w.handleTerminalKey(context.Background(), KeyEvent{Type: KeyRune, Rune: 't'})
	w.handleTerminalKey(context.Background(), KeyEvent{Type: KeyRune, Rune: 'y'})
	w.frameLines(80, 24)
	w.handleTerminalKey(context.Background(), KeyEvent{Type: KeyRune, Rune: '?'})
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 0 || !w.boardHelp.Load() {
		t.Fatalf("an undrawn card took keys: %d choices saved, help %v", n, w.boardHelp.Load())
	}

	c := v2Fixture(t, time.Now())
	defer c.Close()
	c.endOpening()
	row, col := footerCell(t, c, 170, 45, "what is sent")
	c.handleTerminalKey(context.Background(), KeyEvent{Type: KeyMouse, MouseRow: row, MouseColumn: col})
	if !strings.Contains(lastOutput(c), "random installation ID") || c.boardHelp.Load() {
		t.Fatalf("a click on ? what is sent: help %v, output %q", c.boardHelp.Load(), lastOutput(c))
	}
}
