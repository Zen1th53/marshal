package cloud

import (
	"context"
	"runtime"
	"sync"
	"time"
)

// EventKind is the closed set of operational events MARSHAL may report.
//
// It is closed on purpose. An open vocabulary invites a future caller to add a
// kind that carries meaning it should not — "goal_created: refactor the auth
// module" is a sentence about someone's private work. Adding a kind here is a
// deliberate act that has to pass review; adding a string to a map is not.
type EventKind string

const (
	KindRegistration   EventKind = "registration"
	KindAppStart       EventKind = "app_start"
	KindHeartbeat      EventKind = "heartbeat"
	KindModeChange     EventKind = "mode_change"
	KindActivationOK   EventKind = "activation_success"
	KindActivationFail EventKind = "activation_failure"
	KindRenewalOK      EventKind = "renewal_success"
	KindRenewalFail    EventKind = "renewal_failure"
	KindErrorClass     EventKind = "error_class"
)

// TelemetryMode is the operating mode reported alongside an event.
type TelemetryMode string

const (
	TelemetryStandard TelemetryMode = "standard"
	TelemetryUltra    TelemetryMode = "ultra"
)

// ErrorClass is a coarse failure category.
//
// Categories, never messages. An error message is user content: it routinely
// contains a path, a hostname, or a fragment of a command, which is exactly what
// must never leave the machine. Mapping an error to one of these throws that
// detail away at the boundary rather than trusting a later stage to strip it.
type ErrorClass string

const (
	ErrorNetwork      ErrorClass = "network"
	ErrorAuth         ErrorClass = "auth"
	ErrorLeaseExpired ErrorClass = "lease_expired"
	ErrorLeaseInvalid ErrorClass = "lease_invalid"
	ErrorRateLimited  ErrorClass = "rate_limited"
	ErrorServer       ErrorClass = "server"
	ErrorClient       ErrorClass = "client"
	ErrorSandbox      ErrorClass = "sandbox"
	ErrorUnknown      ErrorClass = "unknown"
)

// Event is one privacy-safe operational report.
//
// There is deliberately no map, no free text and no "extra" field. Every value
// is drawn from a closed set or is an opaque identifier, so the worst a buggy
// client can send is a wrong enum — never a prompt, a path or a project name.
// The type system is the privacy control here, not a review checklist.
type Event struct {
	Kind EventKind `json:"kind"`

	InstallationID string `json:"installation_id"`
	SessionID      string `json:"session_id,omitempty"`

	ClientVersion string `json:"client_version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`

	Mode       TelemetryMode `json:"mode,omitempty"`
	ErrorClass ErrorClass    `json:"error_class,omitempty"`

	OccurredAt time.Time `json:"occurred_at"`
}

// maxQueue bounds the client's telemetry backlog.
//
// A queue that grew without limit would turn a long outage into a memory leak,
// and telemetry is the least important thing MARSHAL does. Dropping the oldest
// events is the right trade: recent operational state is what has diagnostic
// value, and none of it is worth a single megabyte of someone's RAM.
const maxQueue = 256

// maxBatch is the largest batch the server accepts.
const maxBatch = 128

// flushInterval is how often a non-empty queue is sent.
const flushInterval = 60 * time.Second

// Reporter queues telemetry and sends it in batches.
//
// A nil Reporter accepts events and discards them, so a caller never needs to
// check whether telemetry is configured before recording something. That is
// what keeps telemetry calls from spreading nil checks through the codebase.
type Reporter struct {
	client  *Client
	version string

	installationID string
	sessionID      string

	mu     sync.Mutex
	queue  []Event
	closed bool
	// running records whether Run is actually executing. Close waits for Run to
	// finish, and without this it would wait for a goroutine that was never
	// started — hanging MARSHAL on exit whenever telemetry was created but the
	// background loop never launched.
	running bool

	// dropped counts events discarded because the queue was full. It is
	// reported to nobody: it exists so a test can prove the bound holds.
	dropped int

	stop chan struct{}
	done chan struct{}
	now  func() time.Time
}

// NewReporter builds a telemetry reporter.
func NewReporter(client *Client, installationID, sessionID, version string, now func() time.Time) *Reporter {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Reporter{
		client:         client,
		version:        version,
		installationID: installationID,
		sessionID:      sessionID,
		stop:           make(chan struct{}),
		done:           make(chan struct{}),
		now:            now,
	}
}

// Record queues an operational event.
//
// The caller supplies a kind and, where the kind allows it, a mode or an error
// class. It cannot supply anything else, so there is no call site anywhere in
// MARSHAL that could attach a path or a message even by mistake.
func (r *Reporter) Record(kind EventKind, mode TelemetryMode, class ErrorClass) {
	if r == nil {
		return
	}
	event := Event{
		Kind:           kind,
		InstallationID: r.installationID,
		SessionID:      r.sessionID,
		ClientVersion:  r.version,
		// Platform and architecture are compile-time constants of the build,
		// not facts discovered about the machine. "linux/amd64" describes the
		// binary; a hostname would describe the person.
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		Mode:       mode,
		ErrorClass: class,
		OccurredAt: r.now(),
	}
	// An error class on a non-error event, or its absence on an error event, is
	// a caller mistake. The server drops such events; dropping them here too
	// means a bug shows up in tests rather than as a silent server-side loss.
	if kind == KindErrorClass && class == "" {
		event.ErrorClass = ErrorUnknown
	}
	if kind != KindErrorClass {
		event.ErrorClass = ""
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	if len(r.queue) >= maxQueue {
		// Drop the oldest. The newest events describe the situation the user is
		// actually in.
		r.queue = r.queue[1:]
		r.dropped++
	}
	r.queue = append(r.queue, event)
}

// Flush sends everything queued, in batches the server will accept.
//
// A send failure returns the batch to the front of the queue rather than
// discarding it, so a brief outage costs nothing. The queue bound is what stops
// that from becoming unbounded growth during a long one.
func (r *Reporter) Flush(ctx context.Context) error {
	if r == nil || r.client == nil {
		return nil
	}
	for {
		r.mu.Lock()
		if len(r.queue) == 0 {
			r.mu.Unlock()
			return nil
		}
		n := min(len(r.queue), maxBatch)
		batch := make([]Event, n)
		copy(batch, r.queue[:n])
		r.queue = r.queue[n:]
		r.mu.Unlock()

		if err := r.client.SendTelemetry(ctx, batch); err != nil {
			r.mu.Lock()
			// Put it back at the front, then re-apply the bound: a long outage
			// must not let the queue grow past its limit by way of retries.
			r.queue = append(batch, r.queue...)
			if len(r.queue) > maxQueue {
				r.dropped += len(r.queue) - maxQueue
				r.queue = r.queue[len(r.queue)-maxQueue:]
			}
			r.mu.Unlock()
			return err
		}
	}
}

// Run flushes on a timer until the context ends or Close is called.
func (r *Reporter) Run(ctx context.Context) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		// Already closed before Run began. Returning without closing done is
		// correct: Close did not wait for us, because it knew we never started.
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()
	defer close(r.done)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stop:
			return
		case <-ticker.C:
			// A failed flush is not reported. Telemetry that complained when it
			// could not be delivered would be worse than telemetry that stays
			// quiet, because the person cannot act on it either way.
			_ = r.Flush(ctx)
		}
	}
}

// Close stops the reporter and makes one final flush attempt.
//
// The attempt is bounded and its failure ignored: MARSHAL must not hang on exit
// because a telemetry endpoint is slow.
func (r *Reporter) Close(ctx context.Context) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	running := r.running
	r.mu.Unlock()

	close(r.stop)
	// Only wait when Run is actually executing. Waiting unconditionally would
	// block forever on a reporter whose background loop was never started,
	// which is the shutdown path of any command that authorizes without
	// launching the workers.
	if running {
		<-r.done
	}

	final, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = r.Flush(final)
}

// Queued reports how many events are waiting, for tests and diagnostics.
func (r *Reporter) Queued() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.queue)
}

// Dropped reports how many events the bound discarded.
func (r *Reporter) Dropped() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dropped
}

// ClassifyError maps an error to a coarse class.
//
// This is where detail is deliberately destroyed. The error's message is never
// read, only its identity, so nothing derived from it can carry a path or a
// command fragment into a report.
func ClassifyError(err error) ErrorClass {
	switch {
	case err == nil:
		return ""
	case isErr(err, ErrUnreachable):
		return ErrorNetwork
	case isErr(err, ErrRefused):
		return ErrorAuth
	case isErr(err, ErrLeaseExpired):
		return ErrorLeaseExpired
	case isErr(err, ErrLeaseInvalid), isErr(err, ErrLeaseSignature),
		isErr(err, ErrLeaseBinding), isErr(err, ErrUnknownKey):
		return ErrorLeaseInvalid
	case isErr(err, ErrState):
		return ErrorClient
	default:
		return ErrorUnknown
	}
}
