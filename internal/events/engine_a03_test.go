package events

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestT43A03ProcessReportsPublishedOnlyAfterCanonicalPersistence(t *testing.T) {
	at := time.Date(2026, 8, 15, 13, 0, 0, 0, time.UTC)
	canonical := Event{ID: "EVENT-A03-1", Sequence: 11, Type: "events.appended", Subject: "system", At: at, Data: map[string]string{"result": "stored"}, IdempotencyKey: "REQ-A03-1"}
	store := &a02EngineStore{stored: canonical}
	bus := &a02EngineBus{store: store}
	engine, err := NewEngine(store, bus)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Process(context.Background(), Event{ID: "EVENT-A03-1", Type: "events.appended", Subject: "system", Data: map[string]string{"result": "stored"}, IdempotencyKey: "REQ-A03-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StatePublished || result.Event.Sequence != 11 || !result.Event.At.Equal(at) {
		t.Fatalf("result=%+v", result)
	}
	if !store.called || !bus.called {
		t.Fatalf("store=%v bus=%v", store.called, bus.called)
	}
}

func TestT43A03ProcessPreservesDurableStateWhenLivePublishFails(t *testing.T) {
	at := time.Date(2026, 8, 15, 13, 1, 0, 0, time.UTC)
	canonical := Event{ID: "EVENT-A03-2", Sequence: 12, Type: "events.appended", Subject: "system", At: at, IdempotencyKey: "REQ-A03-2"}
	store := &a02EngineStore{stored: canonical}
	bus := &a02EngineBus{store: store, err: errors.New("live delivery failed")}
	engine, _ := NewEngine(store, bus)
	result, err := engine.Process(context.Background(), Event{ID: "EVENT-A03-2", Type: "events.appended", Subject: "system", IdempotencyKey: "REQ-A03-2"})
	if err == nil {
		t.Fatal("publish failure unexpectedly succeeded")
	}
	if result.State != StateDurablyAppended || result.Event.Sequence != 12 || !result.Event.At.Equal(at) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestT43A03ProcessStopsAtValidatedWhenStoreFails(t *testing.T) {
	store := &a02EngineStore{err: errors.New("backend unavailable")}
	bus := &a02EngineBus{store: store}
	engine, _ := NewEngine(store, bus)
	result, err := engine.Process(context.Background(), Event{ID: "EVENT-A03-3", Type: "events.appended", Subject: "system", IdempotencyKey: "REQ-A03-3"})
	if err == nil {
		t.Fatal("store failure unexpectedly succeeded")
	}
	if result.State != StateValidated || result.Event.ID != "" || bus.called {
		t.Fatalf("result=%+v bus.called=%v", result, bus.called)
	}
}

type a03ResumeStore struct {
	after Sequence
	limit int
	items []Event
	err   error
}

func (s *a03ResumeStore) Append(context.Context, Event) (Event, error) {
	return Event{}, errors.New("unused")
}
func (s *a03ResumeStore) Since(_ context.Context, after Sequence, limit int) ([]Event, error) {
	s.after, s.limit = after, limit
	out := make([]Event, len(s.items))
	for i := range s.items {
		out[i] = CloneEvent(s.items[i])
	}
	return out, s.err
}

type a03SubscribeBus struct {
	after Sequence
	ch    chan Event
	err   error
}

func (b *a03SubscribeBus) Publish(context.Context, Event) error { return nil }
func (b *a03SubscribeBus) Subscribe(_ context.Context, after Sequence) (<-chan Event, func(), error) {
	b.after = after
	return b.ch, func() {}, b.err
}

func TestT43A03EngineExposesCanonicalResumeAndSubscribeBoundaries(t *testing.T) {
	stored := Event{ID: "EVENT-RESUME", Sequence: 7, Type: "events.appended", Subject: "system", IdempotencyKey: "REQ-RESUME"}
	store := &a03ResumeStore{items: []Event{stored}}
	bus := &a03SubscribeBus{ch: make(chan Event, 1)}
	engine, err := NewEngine(store, bus)
	if err != nil {
		t.Fatal(err)
	}
	items, err := engine.Since(context.Background(), 6, 25)
	if err != nil {
		t.Fatal(err)
	}
	if store.after != 6 || store.limit != 25 || len(items) != 1 || items[0].Sequence != 7 {
		t.Fatalf("store after=%d limit=%d items=%+v", store.after, store.limit, items)
	}
	ch, unsubscribe, err := engine.Subscribe(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if bus.after != 7 || ch == nil || unsubscribe == nil {
		t.Fatalf("bus boundary invalid: after=%d ch_nil=%v unsubscribe_nil=%v", bus.after, ch == nil, unsubscribe == nil)
	}
}

func TestT43A03MalformedEventStopsBeforeDurableStore(t *testing.T) {
	store := &a02EngineStore{}
	bus := &a02EngineBus{store: store}
	engine, _ := NewEngine(store, bus)
	result, err := engine.Process(context.Background(), Event{ID: "EVENT-BAD", Type: "not.registered", Subject: "system", IdempotencyKey: "REQ-BAD"})
	if !errors.Is(err, ErrInvalidType) {
		t.Fatalf("error=%v want=%v", err, ErrInvalidType)
	}
	if result.State != StateProduced || store.called || bus.called {
		t.Fatalf("result=%+v store=%v bus=%v", result, store.called, bus.called)
	}
}

func TestT43A03CancelledProducerStopsBeforeDurableStore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &a02EngineStore{}
	bus := &a02EngineBus{store: store}
	engine, _ := NewEngine(store, bus)
	result, err := engine.Process(ctx, Event{ID: "EVENT-CANCEL", Type: "events.appended", Subject: "system", IdempotencyKey: "REQ-CANCEL"})
	if err == nil || ReasonCode(err) != CodeStoreFailed {
		t.Fatalf("error=%v reason=%q", err, ReasonCode(err))
	}
	if result.State != StateProduced || store.called || bus.called {
		t.Fatalf("result=%+v store=%v bus=%v", result, store.called, bus.called)
	}
}

func TestT43A03IllegalDeliveryTransitionReturnsTypedErrorWithoutMutation(t *testing.T) {
	result := DeliveryResult{State: StatePublished}
	err := advanceDelivery(&result, StateValidated)
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("error=%v want=%v", err, ErrInvalidEvent)
	}
	if result.State != StatePublished {
		t.Fatalf("state mutated to %q", result.State)
	}
}
