package events

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestT43A08SubscriberDropHookCannotStallPublisherIndefinitely(t *testing.T) {
	bus := NewMemoryBus(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, unsubscribe, err := bus.Subscribe(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	_ = ch

	block := make(chan struct{})
	bus.setDropHook(func(context.Context, Event) error {
		<-block
		return nil
	})

	first := Event{ID: "EVENT-A08-BUS-1", Sequence: 1, Type: "events.appended", Subject: "system", IdempotencyKey: "REQ-A08-BUS-1"}
	second := Event{ID: "EVENT-A08-BUS-2", Sequence: 2, Type: "events.appended", Subject: "system", IdempotencyKey: "REQ-A08-BUS-2"}
	if err := bus.Publish(context.Background(), first); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	err = bus.Publish(context.Background(), second)
	elapsed := time.Since(started)
	close(block)
	if !errors.Is(err, ErrStoreFailed) {
		t.Fatalf("Publish error=%v want=%v", err, ErrStoreFailed)
	}
	if elapsed > time.Second {
		t.Fatalf("subscriber drop hook stalled publisher for %v", elapsed)
	}
}

func TestT43A08BlockingDropHookIsBounded(t *testing.T) {
	ctx := context.Background()
	bus := NewMemoryBus(1)
	bus.setDropHook(func(ctx context.Context, _ Event) error {
		<-ctx.Done()
		return ctx.Err()
	})
	ch, unsubscribe, err := bus.Subscribe(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	_ = ch
	first := Event{ID: "EVENT-A08-HOOK-1", Sequence: 1, Type: "events.appended", Subject: "system", IdempotencyKey: "REQ-A08-HOOK-1"}
	second := Event{ID: "EVENT-A08-HOOK-2", Sequence: 2, Type: "events.appended", Subject: "system", IdempotencyKey: "REQ-A08-HOOK-2"}
	if err := bus.Publish(ctx, first); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- bus.Publish(ctx, second) }()
	select {
	case <-done:
		// The exact error is non-authoritative; the invariant is bounded return.
	case <-time.After(400 * time.Millisecond):
		t.Fatal("blocking drop hook stalled live publish")
	}
}
