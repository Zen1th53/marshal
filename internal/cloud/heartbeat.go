package cloud

import (
	"context"
	"errors"
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
// It exists only while ULTRA does. Standard sessions send nothing, because
// presence is a fact about entitlement usage and a Standard user has not asked
// the Cloud for anything.
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

		if !h.gate.Entitled() {
			// No entitlement, nothing to report presence for. Returning rather
			// than idling means a degraded session stops beating for good.
			return
		}

		err := h.client.Heartbeat(ctx, h.state, h.session)
		if err == nil {
			h.reporter.Record(KindHeartbeat, TelemetryUltra, "")
			continue
		}

		h.reporter.Record(KindErrorClass, TelemetryUltra, ClassifyError(err))

		// A refusal means the server has ended this session — revoked, expired
		// or unknown. This is how a revocation reaches a client that is already
		// running, so it degrades rather than retrying.
		if errors.Is(err, ErrRefused) {
			h.gate.Degrade()
			h.reporter.Record(KindModeChange, TelemetryStandard, "")
			return
		}
		// Unreachable is transient: keep the lease and try again next tick.
	}
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
