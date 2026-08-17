package events

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkT43A09ValidationBoundedPayload(b *testing.B) {
	data := make(map[string]string, 64)
	for i := 0; i < 64; i++ {
		data[fmt.Sprintf("k%02d", i)] = "bounded"
	}
	event := Event{ID: "EVENT-BENCH", Type: "events.appended", Subject: "system", Data: data, IdempotencyKey: "REQ-BENCH"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := event.Validate(); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkT43A09PublishFanout32(b *testing.B) {
	ctx := context.Background()
	bus := NewMemoryBus(1024)
	for i := 0; i < 32; i++ {
		_, unsub, err := bus.Subscribe(ctx, 0)
		if err != nil {
			b.Fatal(err)
		}
		defer unsub()
	}
	event := Event{ID: "EVENT-BENCH-PUB", Sequence: 1, Type: "events.appended", Subject: "system", IdempotencyKey: "REQ-BENCH-PUB"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		event.Sequence = Sequence(i + 1)
		if err := bus.Publish(ctx, event); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkT43A09ProcessAuthorized(b *testing.B) {
	ctx := context.Background()
	store := &a02EngineStore{stored: Event{ID: "EVENT-BENCH-PROC", Sequence: 1, Type: "events.appended", Subject: "system", IdempotencyKey: "REQ-BENCH-PROC"}}
	bus := &a02EngineBus{store: store}
	engine, err := newAuthorizedTestEngine(store, bus)
	if err != nil {
		b.Fatal(err)
	}
	event := Event{ID: "EVENT-BENCH-PROC", Type: "events.appended", Subject: "system", IdempotencyKey: "REQ-BENCH-PROC"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.called = false
		bus.called = false
		if _, err := engine.Process(ctx, event); err != nil {
			b.Fatal(err)
		}
	}
}
