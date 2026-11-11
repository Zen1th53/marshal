package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/anim"
)

// openingFakeTicker is an anim.Ticker a test drives by hand: a send on its
// channel is one tick.
type openingFakeTicker struct{ c chan time.Time }

func (t *openingFakeTicker) C() <-chan time.Time { return t.c }
func (t *openingFakeTicker) Stop()               {}

func setOpeningTicker(ft anim.Ticker) func() {
	old := openingNewTicker
	openingNewTicker = func(time.Duration) anim.Ticker { return ft }
	return func() { openingNewTicker = old }
}

func stubOpeningRun(active bool) func() {
	old := openingActiveRun
	openingActiveRun = func(context.Context, *Workspace) bool { return active }
	return func() { openingActiveRun = old }
}

// openingTestWorkspace is a headless workspace the opening treats as eligible:
// the Board home, a theme with animation on, and a terminal large enough.
func openingTestWorkspace(t *testing.T) *Workspace {
	t.Helper()
	t.Setenv("MARSHAL_REDUCED_MOTION", "")
	t.Setenv("TERM", "xterm-256color")
	w := NewWorkspace(nil, "demo", "session")
	w.homeBoard = true
	w.screen = NewScreen(NewTerminal(strings.NewReader(""), &strings.Builder{}))
	return w
}

func openingFrameOf(w *Workspace) int {
	w.opening.mu.Lock()
	defer w.opening.mu.Unlock()
	return w.opening.frame
}

func openingDoneOf(w *Workspace) bool {
	w.opening.mu.Lock()
	defer w.opening.mu.Unlock()
	return w.opening.done
}

func openingActiveOf(w *Workspace) bool {
	w.opening.mu.Lock()
	defer w.opening.mu.Unlock()
	return w.opening.active
}

func waitOpeningRepaint(t *testing.T, w *Workspace) {
	t.Helper()
	select {
	case <-w.repaint:
	case <-time.After(2 * time.Second):
		t.Fatal("no repaint")
	}
}

// openingVisibleLines is the opening's non-blank body lines, stripped of
// colour and surrounding padding, so a test reads exactly what shows.
func openingVisibleLines(v boardVisual, w, h, frame int, checking bool) []string {
	var out []string
	for _, line := range v.openingBody(w, h, frame, checking) {
		if s := strings.TrimSpace(StripANSI(line)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// TestOpeningFrames: frames 1..7 reveal the wordmark one letter at a time,
// and frame 8 adds the tagline under it.
func TestOpeningFrames(t *testing.T) {
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, false)}
	for i := 1; i <= 7; i++ {
		got := openingVisibleLines(v, 60, 12, i, false)
		want := openingWordmark[:i]
		if len(got) != 1 || got[0] != want {
			t.Fatalf("frame %d: %v, want [%q]", i, got, want)
		}
	}
	got := openingVisibleLines(v, 60, 12, 8, false)
	if len(got) != 2 || got[0] != "MARSHAL" || got[1] != "plans · builds · reviews · verifies" {
		t.Fatalf("frame 8: %v", got)
	}
	// The ASCII theme uses " - " for the separator and "..." for the ellipsis.
	ascii := boardVisual{theme: NewTheme(ThemeNoColor, false, false)}
	if g := openingVisibleLines(ascii, 60, 12, 8, false); len(g) != 2 || g[1] != "plans - builds - reviews - verifies" {
		t.Fatalf("ascii frame 8: %v", g)
	}
	if g := openingVisibleLines(ascii, 60, 12, 10, true); len(g) != 2 || g[1] != "Checking this project..." {
		t.Fatalf("ascii checking: %v", g)
	}
}

// TestOpeningMaxDuration: the animation stops at frame 10 (1.25 s at 125 ms a
// frame) and never advances past it, driven by a fake ticker.
func TestOpeningMaxDuration(t *testing.T) {
	w := openingTestWorkspace(t)
	defer stubOpeningRun(false)()
	ft := &openingFakeTicker{c: make(chan time.Time)}
	defer setOpeningTicker(ft)()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.startOpening(ctx)
	waitOpeningRepaint(t, w) // the first paint, frame 1
	if f := openingFrameOf(w); f != 1 {
		t.Fatalf("first frame %d, want 1", f)
	}
	for i := 2; i <= openingFrames; i++ {
		ft.c <- time.Time{}
		waitOpeningRepaint(t, w)
		if f := openingFrameOf(w); f != i {
			t.Fatalf("tick: frame %d, want %d", f, i)
		}
	}
	if !openingDoneOf(w) {
		t.Fatal("the animation is not done at frame 10")
	}
	// The loop has returned; a further tick blocks (no receiver) and never
	// advances the frame past the maximum.
	select {
	case ft.c <- time.Time{}:
		t.Fatal("the loop took a tick after frame 10")
	case <-time.After(100 * time.Millisecond):
	}
	if f := openingFrameOf(w); f != openingFrames {
		t.Fatalf("frame %d exceeded the maximum %d", f, openingFrames)
	}
}

// TestOpeningSkippedOnResume: a stored run that is not closed skips the
// opening, so no frame is ever painted.
func TestOpeningSkippedOnResume(t *testing.T) {
	w := openingTestWorkspace(t)
	defer stubOpeningRun(true)()
	w.startOpening(context.Background())
	if openingActiveOf(w) {
		t.Fatal("the opening played with an active stored run")
	}
}

// TestOpeningSkippedReducedMotion: MARSHAL_REDUCED_MOTION, or a theme with
// animation off, skips the opening.
func TestOpeningSkippedReducedMotion(t *testing.T) {
	t.Run("env", func(t *testing.T) {
		w := openingTestWorkspace(t)
		defer stubOpeningRun(false)()
		t.Setenv("MARSHAL_REDUCED_MOTION", "1")
		w.startOpening(context.Background())
		if openingActiveOf(w) {
			t.Fatal("the opening played under MARSHAL_REDUCED_MOTION")
		}
	})
	t.Run("theme", func(t *testing.T) {
		w := openingTestWorkspace(t)
		defer stubOpeningRun(false)()
		w.theme = NewTheme(ThemeDefault, true, false)
		w.startOpening(context.Background())
		if openingActiveOf(w) {
			t.Fatal("the opening played with animation off")
		}
	})
}

// TestOpeningSkippedSmallTerminal: below 80×24 the opening never plays.
func TestOpeningSkippedSmallTerminal(t *testing.T) {
	w := openingTestWorkspace(t)
	defer stubOpeningRun(false)()
	old := openingTermSize
	openingTermSize = func(*Workspace) (int, int) { return 79, 24 }
	defer func() { openingTermSize = old }()
	w.startOpening(context.Background())
	if openingActiveOf(w) {
		t.Fatal("the opening played on a 79×24 terminal")
	}
}

// TestOpeningKeyEndsAndIsConsumed: a key during the animation ends it and is
// consumed, so m does not reach the Board; q ends it but still quits.
func TestOpeningKeyEndsAndIsConsumed(t *testing.T) {
	w := openingTestWorkspace(t)
	w.opening.active, w.opening.done, w.opening.frame = true, false, 3
	if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'm'}) {
		t.Fatal("m during the animation was not consumed")
	}
	if w.openingAnimating() {
		t.Fatal("m did not end the animation")
	}
	// q ends the animation too, but is not consumed: it falls through to quit.
	w.opening.active, w.opening.done, w.opening.frame = true, false, 3
	if w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'q'}) {
		t.Fatal("q during the animation was consumed instead of quitting")
	}
	if w.openingAnimating() {
		t.Fatal("q did not end the animation")
	}
}

// TestOpeningShowsCheckingAfterAnimation: when the launch check is still
// running at frame 10 the wordmark holds over "Checking this project…"; at
// frame 5 it does not, and once the check is ready the opening ends.
func TestOpeningShowsCheckingAfterAnimation(t *testing.T) {
	w := openingTestWorkspace(t)
	v := boardVisual{theme: NewTheme(ThemeNoColor, true, false)}

	// Frame 10 with the check still running: the checking line shows.
	w.opening.active, w.opening.done, w.opening.frame = true, true, openingFrames
	w.launch.phase = launchChecking
	show, frame, checking := w.openingPaint()
	if !show || frame != openingFrames || !checking {
		t.Fatalf("checking paint: show=%v frame=%d checking=%v", show, frame, checking)
	}
	if text := strings.Join(openingVisibleLines(v, 60, 12, frame, checking), " "); !strings.Contains(text, "Checking this project") {
		t.Fatalf("checking body %q", text)
	}

	// Frame 5, mid-animation: no checking line.
	w.opening.active, w.opening.done, w.opening.frame = true, false, 5
	show, frame, checking = w.openingPaint()
	if !show || frame != 5 || checking {
		t.Fatalf("frame 5 paint: show=%v frame=%d checking=%v", show, frame, checking)
	}

	// The check turns ready: the opening ends and the first-use body takes over.
	w.opening.active, w.opening.done, w.opening.frame = true, true, openingFrames
	w.launch.phase = launchReady
	if show, _, _ = w.openingPaint(); show {
		t.Fatal("the opening kept the body after the check turned ready")
	}
	if openingActiveOf(w) {
		t.Fatal("the opening stayed active after the check turned ready")
	}
}
