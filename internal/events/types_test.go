package events

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestEventContractExposesCanonicalLifecycleFields(t *testing.T) {
	event := Event{
		ID:         "evt-1",
		Sequence:   7,
		Type:       EventTypeTaskCompleted,
		Subject:    "agent-1",
		TaskID:     "task-1",
		RunID:      "run-1",
		ResourceID: "artifact-1",
		EvidenceID: "evidence-1",
		At:         time.Unix(10, 0).UTC(),
		Data:       map[string]any{"result": "ok"},
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !event.Type.Valid() {
		t.Fatal("canonical event type should be valid")
	}
}

func TestEventContractRejectsUnknownTypeWithStableSafeError(t *testing.T) {
	err := (Event{Type: EventType("agent.injected")}).Validate()
	if !errors.Is(err, ErrEventTypeInvalid) {
		t.Fatalf("Validate() error = %v, want ErrEventTypeInvalid", err)
	}
	if got := ReasonCode(err); got != CodeEventTypeInvalid {
		t.Fatalf("ReasonCode() = %q, want %q", got, CodeEventTypeInvalid)
	}
	if got := err.Error(); got != ErrEventTypeInvalid.Error() {
		t.Fatalf("error message = %q, want safe message %q", got, ErrEventTypeInvalid.Error())
	}
}

func TestEventContractRejectsSensitiveDataFieldWithoutLeakingValue(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T43_A01"
	err := (Event{Type: EventTypeTaskCompleted, Data: map[string]any{"api_token": marker}}).Validate()
	if !errors.Is(err, ErrEventSecretField) {
		t.Fatalf("Validate() error = %v, want ErrEventSecretField", err)
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("validation error leaked secret marker: %q", err)
	}
}

func TestEventErrorCodesAreMachineReadableAndUnwrapCauses(t *testing.T) {
	cause := errors.New("private store detail")
	err := NewError(CodeEventStoreFailed, cause)
	if !errors.Is(err, cause) {
		t.Fatal("structured error should retain its cause for errors.Is")
	}
	if ReasonCode(err) != CodeEventStoreFailed {
		t.Fatalf("ReasonCode() = %q, want %q", ReasonCode(err), CodeEventStoreFailed)
	}
	if err.Error() != ErrEventStoreFailed.Error() {
		t.Fatalf("error leaked cause: %q", err)
	}
}

func TestStoreAndBusContractsRequireContext(t *testing.T) {
	var _ Store = contractStore{}
	var _ Bus = contractBus{}
	var _ = context.Background()
}

func TestLifecycleTransitionMatrixRejectsIllegalTransitions(t *testing.T) {
	for _, step := range [][2]LifecycleState{
		{StateProduced, StateValidated},
		{StateValidated, StateDurablyAppended},
		{StateDurablyAppended, StatePublished},
		{StatePublished, StateConsumed},
	} {
		if err := ValidateTransition(step[0], step[1]); err != nil {
			t.Fatalf("ValidateTransition(%q, %q) error = %v", step[0], step[1], err)
		}
	}
	if err := ValidateTransition(StateProduced, StatePublished); !errors.Is(err, ErrEventIllegalTransition) {
		t.Fatalf("illegal transition error = %v, want ErrEventIllegalTransition", err)
	}
}

type contractStore struct{}

func (contractStore) Append(context.Context, Event) (Event, error)     { return Event{}, nil }
func (contractStore) Since(context.Context, Sequence) ([]Event, error) { return nil, nil }

type contractBus struct{}

func (contractBus) Publish(context.Context, Event) error { return nil }
func (contractBus) Subscribe(context.Context, Sequence) (Subscription, error) {
	return Subscription{}, nil
}
