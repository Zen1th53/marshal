package events

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestEventTypeUsesCanonicalClosedRegistry(t *testing.T) {
	for _, eventType := range []Type{"events.appended", "policy.activated", "dag.node.ready", "trustgate.denied"} {
		if !eventType.Valid() {
			t.Fatalf("canonical event type rejected: %q", eventType)
		}
	}
	if Type("provider.trusted").Valid() {
		t.Fatal("unknown provider-controlled event type accepted")
	}
}

func TestEventRejectsUnknownTypeAndSecretField(t *testing.T) {
	base := Event{
		ID:             "EVENT-a01",
		Type:           "events.appended",
		Subject:        "system",
		At:             time.Unix(1, 0).UTC(),
		Data:           map[string]string{"result": "stored"},
		IdempotencyKey: "REQ-a01",
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}

	invalid := base
	invalid.Type = "unknown"
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidType) {
		t.Fatalf("unknown type error = %v, want %v", err, ErrInvalidType)
	}

	invalid = base
	invalid.Data = map[string]string{"token": "not-a-real-token"}
	if err := invalid.Validate(); !errors.Is(err, ErrSecretField) {
		t.Fatalf("secret field error = %v, want %v", err, ErrSecretField)
	}
}

func TestCloneEventDoesNotAliasData(t *testing.T) {
	original := Event{Data: map[string]string{"result": "stored"}}
	clone := CloneEvent(original)
	clone.Data["result"] = "changed"
	if original.Data["result"] != "stored" {
		t.Fatalf("clone mutated source: %#v", original.Data)
	}
}

func TestEventValidationRejectsControlAndOversizedData(t *testing.T) {
	event := Event{ID: "EVENT-a", Type: "events.appended", Subject: "system", Data: map[string]string{"result": "ok\nspoof"}}
	if err := event.Validate(); err == nil {
		t.Fatal("control-bearing data accepted")
	}

	data := make(map[string]string, MaxDataEntries+1)
	for i := 0; i < MaxDataEntries+1; i++ {
		data[string(rune('a'+(i%26)))+strings.Repeat("x", i/26)] = "v"
	}
	event.Data = data
	if err := event.Validate(); err == nil {
		t.Fatal("oversized data map accepted")
	}
}

func TestEventErrorIsMachineReadableAndSecretSafe(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T43_A01_Q9"
	cause := errors.New(marker)
	err := NewError(CodeStoreFailed, cause)
	if !errors.Is(err, cause) || !errors.Is(err, ErrStoreFailed) {
		t.Fatalf("error lost stable identity or cause: %v", err)
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("public error leaked secret marker: %q", err.Error())
	}
	if ReasonCode(err) != CodeStoreFailed {
		t.Fatalf("ReasonCode = %q, want %q", ReasonCode(err), CodeStoreFailed)
	}
}

// compileContracts freezes the provider-neutral A01 store/bus boundary.
func compileContracts(store Store, bus Bus, ctx context.Context) {
	_, _ = store.Append(ctx, Event{})
	_, _ = store.Since(ctx, 0, 100)
	_ = bus.Publish(ctx, Event{})
	_, _, _ = bus.Subscribe(ctx, 0)
}
