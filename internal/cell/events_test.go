package cell

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/events"
)

type memoryCellEvents struct{ events []events.Event }

func (s *memoryCellEvents) Append(_ context.Context, event events.Event) (events.Event, error) {
	if err := event.Validate(); err != nil {
		return events.Event{}, err
	}
	s.events = append(s.events, event)
	return event, nil
}

func (s *memoryCellEvents) Since(context.Context, events.Sequence) ([]events.Event, error) {
	return s.events, nil
}

func TestA05PrepareEmitsDurableLifecycleEventsWithoutRawPayload(t *testing.T) {
	repository := &memoryCellRepository{}
	backend := &countingCellBackend{}
	eventStore := &memoryCellEvents{}
	manager := NewAuditedManager(repository, map[BackendKind]Backend{BackendNative: backend}, allowingCellAuthorizer{}, eventStore)
	_, err := manager.Prepare(context.Background(), Spec{TaskID: "TASK-cell-a05", Workspace: "/tmp/cell-a05", Backend: BackendNative})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(eventStore.events) != 2 || eventStore.events[0].Type != events.EventType("cell.prepare.started") || eventStore.events[1].Type != events.EventType("cell.ready") {
		t.Fatalf("events = %+v, want prepare.started then ready", eventStore.events)
	}
	for _, event := range eventStore.events {
		if event.TaskID != "TASK-cell-a05" || event.ResourceID != "/tmp/cell-a05" || event.Subject != "cell-manager" {
			t.Fatalf("event identity = %+v", event)
		}
		if _, ok := event.Data["workspace"]; ok {
			t.Fatal("raw workspace field persisted in event data")
		}
	}
}
