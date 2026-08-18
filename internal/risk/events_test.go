package risk

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
)

type memoryRiskEventStore struct {
	events []events.Event
}

func (s *memoryRiskEventStore) Append(_ context.Context, event events.Event) (events.Event, error) {
	for _, existing := range s.events {
		if existing.IdempotencyKey == event.IdempotencyKey {
			return existing, nil
		}
	}
	event.Sequence = events.Sequence(len(s.events) + 1)
	event.At = time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	s.events = append(s.events, event)
	return event, nil
}

func (s *memoryRiskEventStore) Since(context.Context, events.Sequence) ([]events.Event, error) {
	return append([]events.Event(nil), s.events...), nil
}

func TestAuditedEngineEmitsReconstructableRiskEventsAfterStateCommit(t *testing.T) {
	store := &memoryAssessmentStore{}
	eventStore := &memoryRiskEventStore{}
	engine := NewAuditedEngine(store, nil, eventStore)
	assessment, err := engine.Assess(context.Background(), AssessmentRequest{
		ID: "assessment-a05",
		Descriptor: ToolDescriptor{
			Tool: "git", Action: "push", Resource: "repo:marshal",
			Factors: Factors{ExternalWrite: true},
		},
		PolicyDigest: "sha256:policy-a05",
	})
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if assessment.State != StateRequirementsEmitted || len(eventStore.events) != 2 {
		t.Fatalf("state=%q events=%d, want requirements_emitted/2", assessment.State, len(eventStore.events))
	}
	if eventStore.events[0].Type != events.EventTypeRiskAssessmentCreated || eventStore.events[1].Type != events.EventTypeRiskLevelHigh {
		t.Fatalf("event types = %q, %q", eventStore.events[0].Type, eventStore.events[1].Type)
	}
	for _, event := range eventStore.events {
		if event.Data["policy_digest"] != "sha256:policy-a05" || event.Data["resource"] != "repo:marshal" {
			t.Fatalf("event data = %#v", event.Data)
		}
	}
	if _, err := engine.Assess(context.Background(), AssessmentRequest{
		ID: "assessment-a05",
		Descriptor: ToolDescriptor{
			Tool: "git", Action: "push", Resource: "repo:marshal",
			Factors: Factors{ExternalWrite: true},
		},
		PolicyDigest: "sha256:policy-a05",
	}); err != nil {
		t.Fatalf("retry Assess: %v", err)
	}
	if len(eventStore.events) != 2 {
		t.Fatalf("retry emitted %d semantic events, want 2", len(eventStore.events))
	}
}
