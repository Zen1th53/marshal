package tui

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// The opening animation is the first screen of a fresh TUI process: the
// wordmark MARSHAL is revealed one letter per frame, then the line
// "plans · builds · reviews · verifies". It replaces the old three-step
// tutorial. The doctor, scan and chat start underneath it (P2-02, P2-14); the
// animation never delays them. It plays only on the Board home with no active
// run, when motion is allowed and the terminal is large enough; otherwise the
// first paint is the ordinary first-use body.

// openingWordmark is the wordmark revealed one letter per frame.
const openingWordmark = "MARSHAL"

// openingFrames is the hard maximum: ten frames at anim.Tick (125 ms) is
// 1.25 s. Frames 1..7 reveal the wordmark, frame 8 adds the tagline, and the
// last frames hold it.
const openingFrames = 10

// openingState is the opening animation's own state. active is set while the
// opening owns the Board body (the moving frames, or the static wordmark held
// over the "Checking this project…" line); done is set once the moving part
// has finished, by reaching the last frame or by a key or click skipping it.
type openingState struct {
	mu     sync.Mutex
	start  time.Time
	frame  int
	active bool
	done   bool
}

// openingNewTicker is the opening loop's tick source; a test replaces it with
// a fake ticker it drives by hand.
var openingNewTicker = func(d time.Duration) anim.Ticker { return anim.RealClock{}.NewTicker(d) }

// openingTermSize is the terminal size the opening's eligibility checks. A
// nil terminal is treated as just large enough, so a headless test workspace
// is eligible; a test replaces it to force a small terminal.
var openingTermSize = func(w *Workspace) (int, int) {
	if w == nil || w.terminal == nil {
		return layout.MinCols, layout.MinRows
	}
	return w.terminal.Size()
}

// openingActiveRun reports whether a Marshal run that is not closed is stored
// for this project: the opening is skipped on resume (P2-12). A read error is
// treated as "no active run". A test replaces it to stub the lookup.
var openingActiveRun = func(ctx context.Context, w *Workspace) bool {
	if w == nil || w.runtime == nil {
		return false
	}
	service := w.runtime.Marshal()
	if service == nil || service.Store == nil {
		return false
	}
	_, record, err := service.Store.LatestMarshalRun(ctx, service.ProjectID)
	if err != nil {
		return false
	}
	return record.Value.State != marshal.Closed
}

// startOpening decides whether the opening may play and, when it may, starts
// the frame loop. When it may not it marks the opening finished at once, so the
// first paint is the ordinary first-use body (or the static wordmark while the
// launch check runs). It never blocks the first paint.
func (w *Workspace) startOpening(ctx context.Context) {
	if !w.openingEligible(ctx) {
		w.opening.mu.Lock()
		w.opening.active, w.opening.done = false, true
		w.opening.mu.Unlock()
		return
	}
	w.opening.mu.Lock()
	w.opening.start = time.Now()
	w.opening.frame = 1
	w.opening.active, w.opening.done = true, false
	w.opening.mu.Unlock()
	w.requestRepaint()
	w.startBackground(ctx, func(ctx context.Context) {
		ticker := openingNewTicker(anim.Tick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C():
				w.opening.mu.Lock()
				if w.opening.done || !w.opening.active {
					w.opening.mu.Unlock()
					return
				}
				w.opening.frame++
				f := w.opening.frame
				if f >= openingFrames {
					w.opening.done = true
				}
				w.opening.mu.Unlock()
				w.requestRepaint()
				if f >= openingFrames {
					return
				}
			}
		}
	})
}

// openingEligible reports whether the opening may play: the Board home with no
// active run, motion allowed, and a terminal of at least 80×24.
func (w *Workspace) openingEligible(ctx context.Context) bool {
	w.mu.RLock()
	home := w.homeBoard
	th := w.theme
	w.mu.RUnlock()
	if !home {
		return false
	}
	// Motion must be allowed: the theme's animation flag, no reduced-motion
	// environment variable, and a terminal that is not "dumb".
	if th == nil || !th.AnimationEnabled {
		return false
	}
	if v := os.Getenv("MARSHAL_REDUCED_MOTION"); v != "" && v != "0" {
		return false
	}
	if strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	if cols, rows := openingTermSize(w); cols < layout.MinCols || rows < layout.MinRows {
		return false
	}
	// The store read is bounded so a slow or unreachable store cannot delay
	// the first paint; on error the opening plays (no active run assumed).
	runCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	return !openingActiveRun(runCtx, w)
}

// endOpening ends the moving animation at once, as a key or click does. The
// static wordmark stays over the "Checking this project…" line while the
// launch check still runs; otherwise the next paint is the first-use body.
func (w *Workspace) endOpening() {
	w.opening.mu.Lock()
	skip := w.opening.active && !w.opening.done
	w.opening.frame = openingFrames
	w.opening.done = true
	w.opening.mu.Unlock()
	if skip {
		w.requestRepaint()
	}
}

// openingAnimating reports whether the moving animation is on screen: a key or
// click during it ends it and is consumed.
func (w *Workspace) openingAnimating() bool {
	w.opening.mu.Lock()
	defer w.opening.mu.Unlock()
	return w.opening.active && !w.opening.done
}

// openingPaint reports whether the opening occupies the Board body now, the
// frame to draw, and whether the "Checking this project…" line shows under the
// wordmark. Once the moving animation has finished it holds the static wordmark
// only while the launch check is still running; when the phase leaves checking
// the opening ends and the first-use body (or the Blocked screen) takes over.
func (w *Workspace) openingPaint() (show bool, frame int, checking bool) {
	w.opening.mu.Lock()
	defer w.opening.mu.Unlock()
	if !w.opening.active {
		return false, 0, false
	}
	if !w.opening.done {
		return true, w.opening.frame, false
	}
	if w.launchCheckingNow() {
		return true, openingFrames, true
	}
	w.opening.active = false
	return false, 0, false
}

// launchCheckingNow reports whether the per-start project check (P2-02) is
// still running.
func (w *Workspace) launchCheckingNow() bool {
	w.launch.mu.Lock()
	defer w.launch.mu.Unlock()
	return w.launch.phase == launchChecking
}

// paintOpening paints the opening in the Board's body with the Board's header
// and footer around it, exactly as the first-use card and Blocked screen do.
func (w *Workspace) paintOpening(l layout.Layout, th *Theme, v boardVisual, frame int, checking bool) []string {
	out := make([]string, l.Rows)
	place := func(id layout.PaneID) {
		if p, ok := l.Pane(id); ok {
			lines := v.render(p)
			for i := 0; i < p.Rect.H && i < len(lines); i++ {
				out[p.Rect.Y+i] = PadCell(lines[i], l.Cols)
			}
		}
	}
	place(layout.Header)
	place(layout.Footer)
	body := l.Body
	if body.H <= 0 {
		return out
	}
	rows := v.openingBody(body.W, body.H, frame, checking)
	for i := 0; i < body.H; i++ {
		s := ""
		if i < len(rows) {
			s = rows[i]
		}
		out[body.Y+i] = PadCell(s, l.Cols)
	}
	if !v.unicode() {
		copy(out[body.Y:body.Y+body.H], v.ascii(out[body.Y:body.Y+body.H], l.Cols))
	}
	return out
}

// openingBody is the opening's centered body for a w-by-h area: the wordmark
// revealed up to the given frame, then the tagline from frame 8, or the
// "Checking this project…" line in place of the tagline while the launch check
// runs past the animation. It is a pure function so a test can render each
// frame.
func (v boardVisual) openingBody(w, h, frame int, checking bool) []string {
	letters := min(max(frame, 0), len(openingWordmark))
	mark := v.bold(v.fg("accent", openingWordmark[:letters]))

	sub := ""
	switch {
	case checking:
		ell := "…"
		if !v.unicode() {
			ell = "..."
		}
		sub = v.fg("dim", "Checking this project"+ell)
	case frame >= 8:
		tagline := "plans · builds · reviews · verifies"
		if !v.unicode() {
			tagline = "plans - builds - reviews - verifies"
		}
		sub = v.fg("dim", tagline)
	}

	content := []string{openingCenter(mark, w)}
	if sub != "" {
		content = append(content, openingCenter(sub, w))
	}

	out := make([]string, 0, h)
	top := max(0, (h-len(content))/2)
	for len(out) < top && len(out) < h {
		out = append(out, PadCell("", w))
	}
	for _, line := range content {
		if len(out) >= h {
			break
		}
		out = append(out, PadCell(line, w))
	}
	for len(out) < h {
		out = append(out, PadCell("", w))
	}
	return out
}

// openingCenter left-pads s so its visible content is centered in w cells.
func openingCenter(s string, w int) string {
	pad := max(0, (w-VisibleLen(s))/2)
	return strings.Repeat(" ", pad) + s
}
