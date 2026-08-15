package events

import (
	"context"
	"testing"
	"time"
)

func TestT43A02MemoryBusPublishesDetachedEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := NewMemoryBus(4)
	ch, unsubscribe, err := bus.Subscribe(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	event := Event{ID: "EVENT-1", Sequence: 1, Type: "events.appended", Subject: "system", Data: map[string]string{"result": "stored"}, IdempotencyKey: "REQ-1"}
	if err := bus.Publish(ctx, event); err != nil {
		t.Fatal(err)
	}
	event.Data["result"] = "mutated"
	select {
	case got := <-ch:
		if got.Data["result"] != "stored" {
			t.Fatalf("published event aliased producer data: %+v", got.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("event was not published")
	}
}
