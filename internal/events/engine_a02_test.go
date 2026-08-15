package events

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type a02EngineStore struct {
	stored Event
	called bool
	err    error
}

func (s *a02EngineStore) Append(context.Context, Event) (Event, error) {
	s.called = true
	return CloneEvent(s.stored), s.err
}

func (s *a02EngineStore) Since(context.Context, Sequence, int) ([]Event, error) {
	return nil, nil
}

type a02EngineBus struct {
	store     *a02EngineStore
	published Event
	called    bool
	err       error
}

func (b *a02EngineBus) Publish(_ context.Context, event Event) error {
	if b.store != nil && !b.store.called {
		return errors.New("publish occurred before durable append")
	}
	b.called = true
	b.published = CloneEvent(event)
	return b.err
}

func (b *a02EngineBus) Subscribe(context.Context, Sequence) (<-chan Event, func(), error) {
	return nil, func() {}, nil
}

func TestT43A02EnginePersistsBeforeLivePublish(t *testing.T) {
	at := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	canonical := Event{
		ID: "EVENT-ENGINE-1", Sequence: 9, Type: "events.appended", Subject: "system",
		At: at, Data: map[string]string{"result": "stored"}, IdempotencyKey: "REQ-ENGINE-1",
	}
	store := &a02EngineStore{stored: canonical}
	bus := &a02EngineBus{store: store}
	engine, err := NewEngine(store, bus)
	if err != nil {
		t.Fatal(err)
	}

	got, err := engine.Append(context.Background(), Event{
		ID: "EVENT-ENGINE-1", Type: "events.appended", Subject: "system",
		Data: map[string]string{"result": "stored"}, IdempotencyKey: "REQ-ENGINE-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !store.called || !bus.called {
		t.Fatalf("store.called=%v bus.called=%v", store.called, bus.called)
	}
	if got.Sequence != canonical.Sequence || !got.At.Equal(at) || bus.published.Sequence != canonical.Sequence || !bus.published.At.Equal(at) {
		t.Fatalf("canonical=%+v got=%+v published=%+v", canonical, got, bus.published)
	}
}

func TestT43A02EngineContainsRawStoreError(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T43_A02_STORE_BACKEND_57de"
	store := &a02EngineStore{err: errors.New(marker)}
	bus := &a02EngineBus{store: store}
	engine, err := NewEngine(store, bus)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Append(context.Background(), Event{
		ID: "EVENT-ENGINE-ERR", Type: "events.appended", Subject: "system", IdempotencyKey: "REQ-ENGINE-ERR",
	})
	if err == nil {
		t.Fatal("raw store failure unexpectedly succeeded")
	}
	if string(ReasonCode(err)) != string(CodeStoreFailed) {
		t.Fatalf("reason=%q want=%q", ReasonCode(err), CodeStoreFailed)
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatal("raw store backend error leaked secret marker")
	}
	if bus.called {
		t.Fatal("live publish executed after durable store failure")
	}
}
