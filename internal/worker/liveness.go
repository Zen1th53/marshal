package worker

import (
	"bytes"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
)

type liveAction int

const (
	liveContinue liveAction = iota
	liveStopDone
	liveStopIdle
)

// livenessTracker turns provider output into progress and completion
// signals: any output is progress, a classified final event is done, and
// while a tool call is in flight (a long check or test run) idle is off.
type livenessTracker struct {
	cfg  adapter.Liveness
	tick time.Duration

	mu       sync.Mutex
	pending  []byte
	last     time.Time
	inflight int
	doneAt   time.Time
	stalled  bool
}

func newLivenessTracker(cfg *adapter.Liveness) *livenessTracker {
	if cfg == nil || (cfg.Classify == nil && cfg.IdleTimeout <= 0 && cfg.StallAfter <= 0) {
		return nil
	}
	t := &livenessTracker{cfg: *cfg, last: time.Now(), tick: time.Second}
	if t.cfg.DoneGrace <= 0 {
		t.cfg.DoneGrace = adapter.DefaultDoneGrace
	}
	for _, d := range []time.Duration{t.cfg.DoneGrace, t.cfg.StallAfter, t.cfg.IdleTimeout} {
		if d > 0 && d/4 < t.tick {
			t.tick = d / 4
		}
	}
	if t.tick < 10*time.Millisecond {
		t.tick = 10 * time.Millisecond
	}
	return t
}

func (t *livenessTracker) observe(p []byte, stdout bool) {
	if t == nil || len(p) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last = time.Now()
	t.stalled = false
	if !stdout || t.cfg.Classify == nil {
		return
	}
	t.pending = append(t.pending, p...)
	for {
		i := bytes.IndexByte(t.pending, '\n')
		if i < 0 {
			break
		}
		line := t.pending[:i]
		switch t.cfg.Classify(line) {
		case adapter.SignalToolStart:
			t.inflight++
		case adapter.SignalToolEnd:
			if t.inflight > 0 {
				t.inflight--
			}
		case adapter.SignalDone:
			if t.doneAt.IsZero() {
				t.doneAt = time.Now()
			}
		}
		t.pending = t.pending[i+1:]
	}
	if len(t.pending) > 4<<20 {
		t.pending = nil // an unterminated huge line is not a stream event
	}
}

func (t *livenessTracker) done() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.doneAt.IsZero()
}

func (t *livenessTracker) check(now time.Time) liveAction {
	t.mu.Lock()
	if !t.doneAt.IsZero() {
		action := liveContinue
		if now.Sub(t.doneAt) >= t.cfg.DoneGrace {
			action = liveStopDone
		}
		t.mu.Unlock()
		return action
	}
	if t.inflight > 0 {
		// A tool call (check, build, test) is running: idle is disabled.
		t.last = now
		t.mu.Unlock()
		return liveContinue
	}
	idle := now.Sub(t.last)
	if t.cfg.IdleTimeout > 0 && idle >= t.cfg.IdleTimeout {
		t.mu.Unlock()
		return liveStopIdle
	}
	notify := t.cfg.StallAfter > 0 && idle >= t.cfg.StallAfter && !t.stalled
	if notify {
		t.stalled = true
	}
	stalled := t.cfg.Stalled
	t.mu.Unlock()
	if notify && stalled != nil {
		stalled(idle)
	}
	return liveContinue
}
