package events

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// AppendAuditEvent creates a bounded projection describing a durable append.
// It deliberately copies identifiers and outcome metadata only; producer
// payloads are never promoted into audit data.
func AppendAuditEvent(event Event, reason, result string) Event {
	data := map[string]any{
		"event_id": event.ID,
		"result":   result,
	}
	for key, value := range map[string]string{
		"subject": event.Subject, "task_id": event.TaskID, "run_id": event.RunID,
		"resource_id": event.ResourceID, "evidence_id": event.EvidenceID, "reason": reason,
	} {
		if value != "" {
			data[key] = value
		}
	}
	return Event{ID: event.ID + ":appended", Sequence: event.Sequence, Type: EventTypeAppended,
		Subject: event.Subject, TaskID: event.TaskID, RunID: event.RunID, ResourceID: event.ResourceID,
		EvidenceID: event.EvidenceID, At: event.At.UTC(), Data: data}
}

// Engine coordinates the durable event store and the live subscriber bus.
// Append commits to Store before publishing, so a lost live delivery can
// always be recovered with Since.
type Engine struct {
	store   Store
	bus     *localBus
	metrics eventMetrics
}

type MetricsSnapshot struct {
	Appended    uint64
	Denied      uint64
	Invalid     uint64
	TotalNanos  uint64
	LastFailure Code
}

type eventMetrics struct {
	appended    atomic.Uint64
	denied      atomic.Uint64
	invalid     atomic.Uint64
	totalNanos  atomic.Uint64
	lastFailure atomic.Value
}

func (e *Engine) Metrics() MetricsSnapshot {
	var last Code
	if value := e.metrics.lastFailure.Load(); value != nil {
		last, _ = value.(Code)
	}
	return MetricsSnapshot{Appended: e.metrics.appended.Load(), Denied: e.metrics.denied.Load(), Invalid: e.metrics.invalid.Load(), TotalNanos: e.metrics.totalNanos.Load(), LastFailure: last}
}

// AppendAuthorized evaluates the owning authorization boundary immediately
// before durable append. Any unavailable, denied, or stale decision fails
// closed and produces no store or publish side effect.
func (e *Engine) AppendAuthorized(ctx context.Context, event Event, authorizer Authorizer) (Event, error) {
	if authorizer == nil {
		e.metrics.denied.Add(1)
		e.metrics.lastFailure.Store(CodeEventAuthorizationUnavailable)
		return Event{}, ErrEventAuthorizationUnavailable
	}
	decision, err := authorizer.Authorize(ctx, event)
	if err != nil {
		e.metrics.denied.Add(1)
		e.metrics.lastFailure.Store(CodeEventAuthorizationUnavailable)
		return Event{}, NewError(CodeEventAuthorizationUnavailable, err)
	}
	if !decision.Allowed {
		e.metrics.denied.Add(1)
		e.metrics.lastFailure.Store(CodeEventAuthorizationDenied)
		return Event{}, ErrEventAuthorizationDenied
	}
	if decision.FreshUntil.IsZero() || !time.Now().UTC().Before(decision.FreshUntil) {
		e.metrics.denied.Add(1)
		e.metrics.lastFailure.Store(CodeEventAuthorizationStale)
		return Event{}, ErrEventAuthorizationStale
	}
	return e.Append(ctx, event)
}

// NewEngine constructs an event coordinator over a durable Store.
func NewEngine(store Store) *Engine {
	return &Engine{store: store, bus: newLocalBus()}
}

// Append validates and durably appends an event, then publishes the stored
// record to current subscribers.
func (e *Engine) Append(ctx context.Context, event Event) (Event, error) {
	started := time.Now()
	if e == nil {
		return Event{}, NewError(CodeEventStoreFailed, fmt.Errorf("event store is unavailable"))
	}
	if e.store == nil {
		e.metrics.invalid.Add(1)
		return Event{}, NewError(CodeEventStoreFailed, fmt.Errorf("event store is unavailable"))
	}
	if err := event.Validate(); err != nil {
		e.metrics.invalid.Add(1)
		e.metrics.lastFailure.Store(ReasonCode(err))
		return Event{}, err
	}
	stored, err := e.store.Append(ctx, event)
	if err != nil {
		e.metrics.invalid.Add(1)
		e.metrics.lastFailure.Store(ReasonCode(err))
		return Event{}, err
	}
	if err := e.bus.Publish(ctx, stored); err != nil {
		e.metrics.lastFailure.Store(ReasonCode(err))
		return Event{}, err
	}
	e.metrics.appended.Add(1)
	e.metrics.totalNanos.Add(uint64(time.Since(started).Nanoseconds()))
	return stored, nil
}

// Since returns durable history after a sequence checkpoint.
func (e *Engine) Since(ctx context.Context, after Sequence) ([]Event, error) {
	if e == nil || e.store == nil {
		return nil, NewError(CodeEventStoreFailed, fmt.Errorf("event store is unavailable"))
	}
	return e.store.Since(ctx, after)
}

func (e *Engine) Publish(ctx context.Context, event Event) error { return e.bus.Publish(ctx, event) }
func (e *Engine) Subscribe(ctx context.Context, after Sequence) (Subscription, error) {
	if e == nil || e.store == nil {
		return Subscription{}, NewError(CodeEventStoreFailed, fmt.Errorf("event store is unavailable"))
	}
	history, err := e.store.Since(ctx, after)
	if err != nil {
		return Subscription{}, err
	}
	return e.bus.subscribe(ctx, history)
}

type localBus struct {
	mu      sync.Mutex
	nextID  uint64
	clients map[uint64]chan Event
}

func newLocalBus() *localBus { return &localBus{clients: make(map[uint64]chan Event)} }

func (b *localBus) Publish(ctx context.Context, event Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.clients {
		select {
		case ch <- event:
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Subscribers are intentionally bounded; durable history remains the
			// recovery path when a consumer cannot keep up.
		}
	}
	return nil
}

func (b *localBus) subscribe(ctx context.Context, history []Event) (Subscription, error) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.nextID++
	id := b.nextID
	for _, event := range history {
		select {
		case ch <- event:
		default:
			b.mu.Unlock()
			close(ch)
			return Subscription{}, NewError(CodeEventStoreFailed, fmt.Errorf("subscriber history exceeds buffer"))
		}
	}
	b.clients[id] = ch
	b.mu.Unlock()

	closeOnce := sync.Once{}
	closeFn := func() {
		closeOnce.Do(func() {
			b.mu.Lock()
			if current, ok := b.clients[id]; ok {
				delete(b.clients, id)
				close(current)
			}
			b.mu.Unlock()
		})
	}
	go func() {
		<-ctx.Done()
		closeFn()
	}()
	return Subscription{Events: ch, Close: closeFn}, nil
}
