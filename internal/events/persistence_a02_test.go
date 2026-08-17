package events

import (
	"context"
	"errors"
	"testing"
	"time"
)

type memoryEventStore struct {
	events []Event
}

func (s *memoryEventStore) Append(_ context.Context, event Event) (Event, error) {
	event.Sequence = Sequence(len(s.events) + 1)
	s.events = append(s.events, event)
	return event, nil
}

func (s *memoryEventStore) Since(_ context.Context, after Sequence) ([]Event, error) {
	if after >= Sequence(len(s.events)) {
		return nil, nil
	}
	return append([]Event(nil), s.events[int(after):]...), nil
}

func TestEngineAppendsDurablyBeforePublishing(t *testing.T) {
	store := &memoryEventStore{}
	engine := NewEngine(store)
	sub, err := engine.Subscribe(context.Background(), 0)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer sub.Close()

	created, err := engine.Append(context.Background(), Event{ID: "evt-1", Type: EventTypeTaskCreated, At: time.Now().UTC()})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if created.Sequence != 1 || len(store.events) != 1 {
		t.Fatalf("durable event = %+v, store length = %d", created, len(store.events))
	}
	select {
	case got := <-sub.Events:
		if got.Sequence != created.Sequence {
			t.Fatalf("published sequence = %d, want %d", got.Sequence, created.Sequence)
		}
	case <-time.After(time.Second):
		t.Fatal("published event was not delivered")
	}
}

func TestEventValidationErrorRemainsTyped(t *testing.T) {
	engine := NewEngine(&memoryEventStore{})
	_, err := engine.Append(context.Background(), Event{Type: EventType("unknown")})
	if !errors.Is(err, ErrEventTypeInvalid) {
		t.Fatalf("Append() error = %v, want ErrEventTypeInvalid", err)
	}
}
