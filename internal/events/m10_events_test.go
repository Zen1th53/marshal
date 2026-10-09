package events

import "testing"

var marshalTypes = []EventType{
	EventTypeMarshalRecommended, EventTypeMarshalPlanApproved, EventTypeMarshalTaskDispatched, EventTypeMarshalUsageCharged,
	EventTypeMarshalTaskHandedIn, EventTypeMarshalTaskAccepted, EventTypeMarshalTaskReturned,
	EventTypeMarshalTaskReassigned, EventTypeMarshalPlanAmended, EventTypeMarshalEscalated,
	EventTypeMarshalTaskMerged, EventTypeMarshalTaskCancelled, EventTypeMarshalRunClosed, EventTypeMarshalVerifierRecorded,
}

func TestM10MarshalEventVocabulary(t *testing.T) {
	for _, kind := range marshalTypes {
		if err := (Event{Type: kind}).Validate(); err != nil {
			t.Errorf("%s: %v", kind, err)
		}
	}
	if err := (Event{Type: "marshal.unknown"}).Validate(); err != ErrEventTypeInvalid {
		t.Fatalf("unknown type: %v", err)
	}
}

func TestM10MarshalEventLifecycle(t *testing.T) {
	for _, kind := range marshalTypes {
		if !kind.Valid() {
			t.Fatalf("%s invalid", kind)
		}
		for _, edge := range [][2]LifecycleState{{StateProduced, StateValidated}, {StateValidated, StateDurablyAppended}, {StateDurablyAppended, StatePublished}, {StatePublished, StateConsumed}} {
			if err := ValidateTransition(edge[0], edge[1]); err != nil {
				t.Errorf("%s %s -> %s: %v", kind, edge[0], edge[1], err)
			}
		}
	}
}
