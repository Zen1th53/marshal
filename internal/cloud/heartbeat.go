package cloud

import (
	"context"
	"sync"
	"time"
)

// heartbeatInterval is how often a live ULTRA session reports presence.
//
// It must be comfortably shorter than the server's presence window, or a
// healthy client would be counted as gone between beats. The server treats an
// installation as present for six minutes, so two minutes leaves room for two
// missed beats before presence lapses — enough to ride out a brief outage
// without pretending a genuinely dead client is alive.
const heartbeatInterval = 2 * time.Minute

// Heartbeat reports presence for a live ULTRA session.
//
// This is the ULTRA authority heartbeat. Both modes send operational telemetry
// separately; opting out of telemetry does not disable lease validation.
type Heartbeat struct {
	client   *Client
	gate     *Gate
	reporter *Reporter
	state    State
	session  string

	mu      sync.Mutex
	stopped bool
	// running records whether Run is actually executing. Stop waits for Run to
	// finish, and without this it would wait for a goroutine that was never
	// started — hanging MARSHAL on exit whenever a session ended before the
	// heartbeat began.
	running bool
	stop    chan struct{}
	done    chan struct{}
}

// NewHeartbeat prepares a heartbeat driver. It sends nothing until Run.
func NewHeartbeat(client *Client, gate *Gate, reporter *Reporter, state State, sessionID string) *Heartbeat {
	return &Heartbeat{
		client: client, gate: gate, reporter: reporter,
		state: state, session: sessionID,
		stop: make(chan struct{}), done: make(chan struct{}),
	}
}

// Run beats until the context ends, Stop is called, or entitlement lapses.
//
// The gate is consulted before every beat rather than once at the start. A
// session that degrades to Standard stops reporting presence immediately, so the
// server's count reflects live ULTRA sessions rather than sessions that were
// once ULTRA.
func (h *Heartbeat) Run(ctx context.Context) {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.stopped {
		// Already stopped before Run began. Returning without closing done is
		// correct: Stop did not wait for us, because it knew we never started.
		h.mu.Unlock()
		return
	}
	h.running = true
	h.mu.Unlock()
	defer close(h.done)

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-h.stop:
			return
		case <-ticker.C:
		}

		if !h.beat(ctx) {
			return
		}
	}
}

// beat returns false once the connected authority is lost.
func (h *Heartbeat) beat(ctx context.Context) bool {
	if !h.gate.Entitled() {
		return false
	}
	err := h.client.Heartbeat(ctx, h.state, h.session)
	if err == nil {
		h.reporter.Record(KindHeartbeat, TelemetryUltra, "")
		return true
	}
	h.reporter.Record(KindErrorClass, TelemetryUltra, ClassifyError(err))
	h.gate.DegradeBecause(err)
	h.reporter.Record(KindModeChange, TelemetryStandard, "")
	return false
}

// Stop ends the heartbeat.
func (h *Heartbeat) Stop() {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return
	}
	h.stopped = true
	running := h.running
	h.mu.Unlock()

	close(h.stop)
	// Only wait when Run is actually executing. Waiting unconditionally would
	// block forever on a session that ended before its heartbeat started, which
	// is exactly the shutdown path a short-lived command takes.
	if running {
		<-h.done
	}
}
