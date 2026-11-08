// Package anim is the Board's tick source. It owns one ticker that runs only
// while the projection holds something that moves: a spinning lane or task, a
// state-change pulse that has not yet faded, or a sparkline change waiting for
// its next refresh. When none remains the ticker is stopped and its goroutine
// exits, so an idle Board costs no wakeups and no redraws.
//
// The animator never paints. It asks for a repaint by a non-blocking send on
// the workspace's repaint channel and hands the painter a Frame to read.
package anim

import (
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

const (
	// Tick is the frame period while something animates: 8 fps.
	Tick = 125 * time.Millisecond
	// Pulse is how long a row whose state changed is drawn bold.
	Pulse = 300 * time.Millisecond
	// SparkEvery is the sparkline refresh period; charges arriving faster are
	// held until the next refresh, never shown per frame.
	SparkEvery = 5 * time.Second
)

// Spinner frame sets. Braille needs a UTF-8 terminal; ASCII is the fallback.
var (
	Braille = [...]string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}
	ASCII   = [...]string{"|", "/", "-", `\`}
)

// UTF8 reports whether the locale the environment names is UTF-8, by the
// POSIX precedence LC_ALL, LC_CTYPE, LANG. getenv is os.Getenv in production.
func UTF8(getenv func(string) string) bool {
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := getenv(k); v != "" {
			v = strings.ToLower(v)
			return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
		}
	}
	return false
}

// Clock is the animator's time source. Tests substitute a manual one.
type Clock interface {
	Now() time.Time
	NewTicker(time.Duration) Ticker
}

// Ticker is the part of time.Ticker the animator uses.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// RealClock is the wall clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) NewTicker(d time.Duration) Ticker { return realTicker{time.NewTicker(d)} }

type realTicker struct{ *time.Ticker }

func (t realTicker) C() <-chan time.Time { return t.Ticker.C }

// Frame is what the painter reads for one redraw. It is a copy: the animator
// keeps ticking while a Frame is being drawn.
type Frame struct {
	N     uint64              // ticks so far; selects the spinner glyph
	pulse map[string]bool     // task IDs drawn bold
	spark map[string][8]uint8 // worker -> sparkline, refreshed every SparkEvery
}

// Spinner is the glyph for this frame.
func (f Frame) Spinner(utf8 bool) string {
	if utf8 {
		return Braille[f.N%uint64(len(Braille))]
	}
	return ASCII[f.N%uint64(len(ASCII))]
}

// Pulsing reports whether the task's row is drawn bold: its state changed
// less than Pulse ago.
func (f Frame) Pulsing(taskID string) bool { return f.pulse[taskID] }

// Spark is the lane's sparkline as last refreshed, eight levels 0 to 7.
func (f Frame) Spark(worker string) [8]uint8 { return f.spark[worker] }

// Animator turns successive Boards into ticks. All methods are safe for
// concurrent use.
type Animator struct {
	clock   Clock
	repaint chan<- struct{}

	mu       sync.Mutex
	n        uint64
	spinning bool
	states   map[string]model.TaskState // previous Board's task states
	pulses   map[string]time.Time       // task ID -> end of its pulse
	latest   map[string][8]int64        // worker -> Recent of the newest Board
	shown    map[string][8]int64        // worker -> Recent as last refreshed
	sparked  time.Time                  // last sparkline refresh
	stop     chan struct{}              // non-nil while a ticker runs
	closed   bool
}

// New returns an idle animator that asks for repaints on repaint.
func New(repaint chan<- struct{}, clock Clock) *Animator {
	return &Animator{clock: clock, repaint: repaint, pulses: map[string]time.Time{}}
}

// Update takes a new projection. It starts the ticker when the Board holds
// something that moves and stops it at once when nothing does. It does not
// itself ask for a repaint: whoever published the Board already did.
func (a *Animator) Update(b model.Board) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.clock.Now()

	a.spinning = false
	if !b.Stale {
		// A stale Board is the last projection kept on screen: nothing on it
		// is known to be moving.
		for _, l := range b.Lanes {
			a.spinning = a.spinning || l.State == model.LaneWorking || l.State == model.LaneReview
		}
		// A working or review task spins only while a live lane is on it;
		// one without, as a recovered run shows, is stalled and drawn still.
		for _, t := range b.Tasks {
			a.spinning = a.spinning || (t.State == model.TaskWorking || t.State == model.TaskReview) && b.Live(t.ID)
		}
	}

	states := make(map[string]model.TaskState, len(b.Tasks))
	for _, t := range b.Tasks {
		states[t.ID] = t.State
		if prev, ok := a.states[t.ID]; ok && prev != t.State {
			a.pulses[t.ID] = now.Add(Pulse)
		}
	}
	for id := range a.pulses {
		if _, ok := states[id]; !ok {
			delete(a.pulses, id)
		}
	}
	a.states = states

	// A lane without charges draws an empty sparkline either way; leaving it
	// out keeps a new lane from holding the ticker until the next refresh.
	a.latest = make(map[string][8]int64, len(b.Lanes))
	for _, l := range b.Lanes {
		if l.Recent != ([8]int64{}) {
			a.latest[l.Worker] = l.Recent
		}
	}
	// A Board with no charged lane, such as the empty one handed over
	// when the Board is hidden, has nothing left to show: drop the
	// sparklines at once rather than keep a ticker for the next refresh.
	if a.sparkPending() && (len(a.latest) == 0 || now.Sub(a.sparked) >= SparkEvery) {
		a.refreshSpark(now)
	}
	a.schedule()
}

// Frame returns the state to paint now.
func (a *Animator) Frame() Frame {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := Frame{N: a.n, pulse: make(map[string]bool, len(a.pulses)), spark: make(map[string][8]uint8, len(a.shown))}
	for id := range a.pulses {
		f.pulse[id] = true
	}
	for w, r := range a.shown {
		f.spark[w] = model.Lane{Recent: r}.Spark()
	}
	return f
}

// Running reports whether a ticker is live.
func (a *Animator) Running() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stop != nil
}

// Close stops the ticker for good; later Updates start nothing.
func (a *Animator) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	a.halt()
}

// active reports whether anything needs a future tick. Caller holds mu.
func (a *Animator) active() bool {
	return a.spinning || len(a.pulses) > 0 || a.sparkPending()
}

// sparkPending reports whether a lane's charges changed since the last
// refresh. Caller holds mu.
func (a *Animator) sparkPending() bool { return !maps.Equal(a.latest, a.shown) }

func (a *Animator) refreshSpark(now time.Time) {
	a.shown = maps.Clone(a.latest)
	a.sparked = now
}

// schedule starts or stops the ticker to match active. Caller holds mu.
func (a *Animator) schedule() {
	switch {
	case a.closed || !a.active():
		a.halt()
	case a.stop == nil:
		a.stop = make(chan struct{})
		go a.run(a.clock.NewTicker(Tick), a.stop)
	}
}

func (a *Animator) halt() {
	if a.stop != nil {
		close(a.stop)
		a.stop = nil
	}
}

func (a *Animator) run(t Ticker, stop <-chan struct{}) {
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C():
			if !a.tick(stop) {
				return
			}
		}
	}
}

// tick advances one frame and asks for a repaint when the frame differs from
// the last. It reports whether the ticker should keep running; the last tick
// still repaints, so a fading pulse is drawn plain before the ticker stops.
func (a *Animator) tick(stop <-chan struct{}) bool {
	a.mu.Lock()
	if a.stop != stop {
		// Update halted this ticker while the tick was in flight.
		a.mu.Unlock()
		return false
	}
	now := a.clock.Now()
	changed := a.spinning
	if a.spinning {
		a.n++
	}
	for id, end := range a.pulses {
		if !now.Before(end) {
			delete(a.pulses, id)
			changed = true
		}
	}
	if a.sparkPending() && now.Sub(a.sparked) >= SparkEvery {
		a.refreshSpark(now)
		changed = true
	}
	alive := a.active()
	if !alive {
		a.stop = nil
	}
	a.mu.Unlock()
	if changed {
		select {
		case a.repaint <- struct{}{}:
		default:
		}
	}
	return alive
}
