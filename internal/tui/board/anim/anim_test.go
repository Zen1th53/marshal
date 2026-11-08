package anim

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/model"
)

var epoch = time.Unix(1_800_000_000, 0).UTC()

// manual is a clock the test moves by hand. Its tickers never fire: the test
// calls tick itself, so every step is deterministic.
type manual struct {
	mu      sync.Mutex
	now     time.Time
	made    int
	stopped chan struct{}
}

func newManual() *manual { return &manual{now: epoch, stopped: make(chan struct{}, 16)} }

func (m *manual) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

func (m *manual) advance(d time.Duration) {
	m.mu.Lock()
	m.now = m.now.Add(d)
	m.mu.Unlock()
}

func (m *manual) NewTicker(time.Duration) Ticker {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.made++
	return silent{m}
}

func (m *manual) tickers() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.made
}

type silent struct{ m *manual }

func (silent) C() <-chan time.Time { return nil }
func (s silent) Stop()             { s.m.stopped <- struct{}{} }

func setup() (*Animator, *manual, chan struct{}) {
	clock := newManual()
	repaint := make(chan struct{}, 1)
	return New(repaint, clock), clock, repaint
}

// step advances the clock by one Tick and runs the live ticker's tick.
func step(t *testing.T, a *Animator, clock *manual) bool {
	t.Helper()
	clock.advance(Tick)
	a.mu.Lock()
	stop := a.stop
	a.mu.Unlock()
	if stop == nil {
		t.Fatal("step without a running ticker")
	}
	return a.tick(stop)
}

func drained(repaint chan struct{}) bool {
	select {
	case <-repaint:
		return true
	default:
		return false
	}
}

func board(lanes []model.Lane, tasks ...model.TaskRow) model.Board {
	return model.Board{Lanes: lanes, Tasks: tasks}
}

func row(id string, s model.TaskState) model.TaskRow { return model.TaskRow{ID: id, State: s} }

func idleBoard() model.Board {
	return board([]model.Lane{{Worker: "codex", TaskID: "T-3", State: model.LaneIdle}},
		row("T-1", model.TaskQueued), row("T-2", model.TaskMerged), row("T-3", model.TaskParked), row("T-4", model.TaskAccepted))
}

// Acceptance: an idle Board left alone for 10 s writes no frame. The Board is
// republished every 2 s, as the session coordinator and the Marshal status
// read do, which is how the 719232a class of bug kept an idle TUI busy.
func TestIdleBoardTenSecondsNoFrames(t *testing.T) {
	a, clock, repaint := setup()
	for elapsed := time.Duration(0); elapsed <= 10*time.Second; elapsed += Tick {
		if elapsed%(2*time.Second) == 0 {
			a.Update(idleBoard())
		}
		clock.advance(Tick)
	}
	if n := clock.tickers(); n != 0 {
		t.Fatalf("idle Board started %d tickers", n)
	}
	if a.Running() {
		t.Fatal("idle Board left a ticker running")
	}
	if drained(repaint) {
		t.Fatal("idle Board asked for a repaint")
	}
}

func TestStaleBoardDoesNotSpin(t *testing.T) {
	a, clock, _ := setup()
	b := board([]model.Lane{{Worker: "codex", TaskID: "T-1", State: model.LaneWorking}}, row("T-1", model.TaskWorking))
	b.Stale = true
	a.Update(b)
	if clock.tickers() != 0 || a.Running() {
		t.Fatal("a stale Board animated")
	}
}

func TestSpinnerTicksOnlyWhileSomethingWorks(t *testing.T) {
	for _, b := range []model.Board{
		board([]model.Lane{{Worker: "codex", TaskID: "T-1", State: model.LaneWorking}}),
		board([]model.Lane{{Worker: "codex", TaskID: "T-1", State: model.LaneReview}}),
		board([]model.Lane{{Worker: "codex", TaskID: "T-1", State: model.LaneWorking}}, row("T-1", model.TaskWorking)),
		board([]model.Lane{{Worker: "codex", TaskID: "T-1", State: model.LaneReview}}, row("T-1", model.TaskReview)),
	} {
		a, clock, repaint := setup()
		a.Update(b)
		if !a.Running() || clock.tickers() != 1 {
			t.Fatalf("%+v: no ticker", b)
		}
		for i := 1; i <= 3; i++ {
			if !step(t, a, clock) {
				t.Fatal("ticker stopped while working")
			}
			if !drained(repaint) {
				t.Fatalf("tick %d asked for no repaint", i)
			}
			if f := a.Frame(); f.N != uint64(i) {
				t.Fatalf("frame %d after %d ticks", f.N, i)
			}
		}
		// Republishing the same Board must not start a second ticker.
		a.Update(b)
		if clock.tickers() != 1 {
			t.Fatalf("republish started %d tickers", clock.tickers())
		}
		a.Close()
	}
}

// A recovered run shows tasks working or in review with no live lane on
// them. Such a task is stalled: nothing moves, so nothing may spin.
func TestStalledTaskDoesNotSpin(t *testing.T) {
	for name, b := range map[string]model.Board{
		"no lanes":          board(nil, row("T-1", model.TaskWorking), row("T-2", model.TaskReview)),
		"idle lane on task": board([]model.Lane{{Worker: "codex", TaskID: "T-1", State: model.LaneIdle}}, row("T-1", model.TaskWorking)),
		"lane elsewhere":    board([]model.Lane{{Worker: "codex", TaskID: "T-9", State: model.LaneParked}}, row("T-1", model.TaskReview)),
	} {
		a, clock, repaint := setup()
		for range 3 {
			a.Update(b)
		}
		if a.Running() || clock.tickers() != 0 || drained(repaint) {
			t.Fatalf("%s: a stalled task animated", name)
		}
		if f := a.Frame(); f.N != 0 {
			t.Fatalf("%s: frame moved to %d", name, f.N)
		}
		a.Close()
	}
}

// Invariant: the animator stops within one tick after the last animated
// element leaves. Leaving through Update stops it at once.
func TestTickerStopsWhenWorkEnds(t *testing.T) {
	a, clock, _ := setup()
	working := board([]model.Lane{{Worker: "codex", TaskID: "T-1", State: model.LaneWorking}})
	a.Update(working)
	step(t, a, clock)
	a.mu.Lock()
	stop := a.stop
	a.mu.Unlock()
	a.Update(board([]model.Lane{{Worker: "codex", TaskID: "T-1", State: model.LaneIdle}}))
	if a.Running() {
		t.Fatal("ticker still running after the last working lane left")
	}
	select {
	case <-clock.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("ticker goroutine did not stop its ticker")
	}
	if a.tick(stop) {
		t.Fatal("a tick in flight on a halted ticker kept it alive")
	}
	f := a.Frame()
	a.Update(working)
	if clock.tickers() != 2 {
		t.Fatal("new work did not restart the ticker")
	}
	if a.Frame().N != f.N {
		t.Fatal("restart moved the frame without a tick")
	}
}

func TestPulseFadesAfter300msThenTickerStops(t *testing.T) {
	a, clock, repaint := setup()
	a.Update(board(nil, row("T-1", model.TaskAccepted), row("T-2", model.TaskQueued)))
	if a.Running() {
		t.Fatal("first projection pulsed")
	}
	a.Update(board(nil, row("T-1", model.TaskMerged), row("T-2", model.TaskQueued)))
	f := a.Frame()
	if !f.Pulsing("T-1") || f.Pulsing("T-2") {
		t.Fatalf("pulse T-1=%v T-2=%v", f.Pulsing("T-1"), f.Pulsing("T-2"))
	}
	if !a.Running() {
		t.Fatal("a pulse did not start the ticker")
	}
	// 125 and 250 ms: still bold, nothing to redraw.
	for range 2 {
		if !step(t, a, clock) || drained(repaint) {
			t.Fatal("pulse ended early or repainted without a change")
		}
	}
	// 375 ms: the pulse has faded; one repaint draws the row plain and the
	// ticker stops on the same tick.
	if step(t, a, clock) {
		t.Fatal("ticker kept running after the pulse faded")
	}
	if !drained(repaint) {
		t.Fatal("fading pulse was not repainted")
	}
	if a.Frame().Pulsing("T-1") || a.Running() {
		t.Fatal("pulse or ticker outlived 300 ms")
	}
}

func TestPulseForgetsRemovedTask(t *testing.T) {
	a, _, _ := setup()
	a.Update(board(nil, row("T-1", model.TaskAccepted)))
	a.Update(board(nil, row("T-1", model.TaskMerged)))
	a.Update(board(nil))
	if a.Running() || a.Frame().Pulsing("T-1") {
		t.Fatal("a removed task kept its pulse")
	}
}

func lane(recent ...int64) model.Lane {
	l := model.Lane{Worker: "codex", TaskID: "T-1", State: model.LaneParked}
	copy(l.Recent[8-len(recent):], recent)
	return l
}

func TestSparklineRefreshesAtMostEveryFiveSeconds(t *testing.T) {
	a, clock, repaint := setup()
	a.Update(board([]model.Lane{lane(10)}))
	if got := a.Frame().Spark("codex"); got[7] != 7 {
		t.Fatalf("first charge not shown: %v", got)
	}
	if a.Running() {
		t.Fatal("a refreshed sparkline kept the ticker")
	}
	clock.advance(time.Second)
	a.Update(board([]model.Lane{lane(10, 20)}))
	if got := a.Frame().Spark("codex"); got[6] != 0 {
		t.Fatalf("charge 1 s after the last refresh shown at once: %v", got)
	}
	if !a.Running() {
		t.Fatal("a pending sparkline did not keep a ticker")
	}
	ticks := 0
	for step(t, a, clock) {
		ticks++
		if drained(repaint) {
			t.Fatal("repaint before the sparkline refresh")
		}
	}
	if !drained(repaint) {
		t.Fatal("sparkline refresh not repainted")
	}
	if got := a.Frame().Spark("codex"); got[6] != 3 || got[7] != 7 {
		t.Fatalf("sparkline after refresh: %v", got)
	}
	if want := int((4*time.Second)/Tick) - 1; ticks != want {
		t.Fatalf("refresh after %d ticks, want %d", ticks+1, want+1)
	}
}

func TestLaneWithoutChargesHoldsNoTicker(t *testing.T) {
	a, _, _ := setup()
	a.Update(board([]model.Lane{lane(10)}))
	a.Update(board([]model.Lane{lane(10), {Worker: "claude", TaskID: "T-2", State: model.LaneIdle}}))
	if a.Running() {
		t.Fatal("a lane with no charges waited for a sparkline refresh")
	}
}

func TestCloseStopsForGood(t *testing.T) {
	a, clock, _ := setup()
	working := board([]model.Lane{{Worker: "codex", TaskID: "T-1", State: model.LaneWorking}}, row("T-1", model.TaskWorking))
	a.Update(working)
	a.Close()
	if a.Running() {
		t.Fatal("Close left the ticker")
	}
	a.Update(working)
	if a.Running() || clock.tickers() != 1 {
		t.Fatal("Update after Close started a ticker")
	}
}

func TestSpinnerFrames(t *testing.T) {
	seen := map[string]bool{}
	for n := range uint64(8) {
		seen[Frame{N: n}.Spinner(true)] = true
	}
	if len(seen) != 8 || !seen["⣾"] {
		t.Fatalf("braille frames %v", seen)
	}
	if g := (Frame{N: 5}).Spinner(false); g != "/" {
		t.Fatalf("ASCII frame 5 = %q", g)
	}
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, c := range []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{"LANG": "en_US.UTF-8"}, true},
		{map[string]string{"LC_CTYPE": "C.utf8", "LANG": "C"}, true},
		{map[string]string{"LC_ALL": "C", "LANG": "en_US.UTF-8"}, false},
		{map[string]string{}, false},
	} {
		if got := UTF8(env(c.env)); got != c.want {
			t.Fatalf("UTF8(%v) = %v", c.env, got)
		}
	}
}

// The real ticker: frames arrive while a lane works, and none after it stops.
func TestRealClockTicksThenFallsSilent(t *testing.T) {
	repaint := make(chan struct{}, 1)
	a := New(repaint, RealClock{})
	defer a.Close()
	a.Update(board([]model.Lane{{Worker: "codex", TaskID: "T-1", State: model.LaneWorking}}, row("T-1", model.TaskWorking)))
	for i := range 3 {
		select {
		case <-repaint:
		case <-time.After(5 * time.Second):
			t.Fatalf("no frame %d while working", i)
		}
	}
	a.Update(board(nil, row("T-1", model.TaskMerged)))
	// The merge pulses for 300 ms; let it fade, then expect silence.
	time.Sleep(Pulse + 2*Tick)
	drained(repaint)
	if a.Running() {
		t.Fatal("ticker running after work and pulse ended")
	}
	select {
	case <-repaint:
		t.Fatal("frame after the animator stopped")
	case <-time.After(3 * Tick):
	}
}

func twelveLanes() model.Board {
	var b model.Board
	for i := range 12 {
		id := fmt.Sprintf("T-%02d", i)
		l := model.Lane{Worker: fmt.Sprintf("w%02d", i), TaskID: id, State: model.LaneWorking}
		for j := range l.Recent {
			l.Recent[j] = int64(1000 * (i + j + 1))
		}
		b.Lanes = append(b.Lanes, l)
		b.Tasks = append(b.Tasks, row(id, model.TaskWorking))
	}
	return b
}

// Invariant: under 2% of one core with 12 animated lanes. At 8 frames a
// second that leaves 2.5 ms per frame for the animator's own work (tick and
// the Frame the painter reads); it uses a few microseconds.
func TestTwelveLanesCPUBudget(t *testing.T) {
	a, clock, repaint := setup()
	a.Update(twelveLanes())
	const frames = 800 // 100 s of animation
	start := time.Now()
	for range frames {
		step(t, a, clock)
		drained(repaint)
		_ = a.Frame()
	}
	per := time.Since(start) / frames
	budget := time.Duration(float64(time.Second) * 0.02 / (float64(time.Second) / float64(Tick)))
	t.Logf("%v per frame, %.3f%% of one core at 8 fps", per, 100*per.Seconds()*float64(time.Second/Tick))
	if per > budget {
		t.Fatalf("%v per frame exceeds %v", per, budget)
	}
}

func BenchmarkTickTwelveLanes(b *testing.B) {
	clock := newManual()
	a := New(make(chan struct{}, 1), clock)
	a.Update(twelveLanes())
	a.mu.Lock()
	stop := a.stop
	a.mu.Unlock()
	b.ReportAllocs()
	for b.Loop() {
		clock.advance(Tick)
		a.tick(stop)
		_ = a.Frame()
	}
}

func BenchmarkUpdateTwelveLanes(b *testing.B) {
	a := New(make(chan struct{}, 1), newManual())
	defer a.Close()
	tw := twelveLanes()
	b.ReportAllocs()
	for b.Loop() {
		a.Update(tw)
	}
}
