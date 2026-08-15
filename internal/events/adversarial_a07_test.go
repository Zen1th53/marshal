package events

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestT43A07PassEventsRequireEvidenceAndSecretFieldsFailClosed(t *testing.T) {
	cases := []struct {
		name  string
		event Event
		want  error
	}{
		{"pass-missing-evidence", Event{ID: "EVENT-A07-PASS", Type: "evaluation.stage.passed", Subject: "SUBJECT-A07", IdempotencyKey: "REQ-A07-PASS"}, ErrEvidenceRequired},
		{"secret-key", Event{ID: "EVENT-A07-SECRET", Type: "events.appended", Subject: "SUBJECT-A07", IdempotencyKey: "REQ-A07-SECRET", Data: map[string]string{"token": "MARSHAL_TEST_SECRET_T43_A07_6d5f"}}, ErrSecretField},
		{"control-value", Event{ID: "EVENT-A07-CONTROL", Type: "events.appended", Subject: "SUBJECT-A07", IdempotencyKey: "REQ-A07-CONTROL", Data: map[string]string{"result": "ok\nspoofed"}}, ErrInvalidEvent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.event.Validate()
			if !errors.Is(err, tc.want) {
				t.Fatalf("Validate() error=%v want=%v", err, tc.want)
			}
			if err != nil && strings.Contains(err.Error(), "MARSHAL_TEST_SECRET_T43_A07_6d5f") {
				t.Fatalf("public error leaked marker: %q", err.Error())
			}
		})
	}
}

func TestT43A07MemoryBusNeverPublishesSequenceAtOrBeforeSubscriberCursor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := NewMemoryBus(4)
	ch, unsubscribe, err := bus.Subscribe(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	for _, seq := range []Sequence{9, 10, 11, 12} {
		event := Event{ID: EventID("EVENT-A07-BUS-" + strconv.FormatUint(uint64(seq), 10)), Sequence: seq, Type: "events.appended", Subject: "SUBJECT-A07", IdempotencyKey: IdempotencyKey("REQ-A07-BUS-" + strconv.FormatUint(uint64(seq), 10))}
		if err := bus.Publish(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	first := <-ch
	second := <-ch
	if first.Sequence != 11 || second.Sequence != 12 {
		t.Fatalf("received sequences=%d,%d want=11,12", first.Sequence, second.Sequence)
	}
	select {
	case extra := <-ch:
		t.Fatalf("unexpected replay sequence=%d", extra.Sequence)
	default:
	}
}

func FuzzT43A07EventValidationNeverAcceptsUnknownTypeOrControlText(f *testing.F) {
	f.Add("events.appended", "SUBJECT-A07", "result", "stored")
	f.Add("unknown.event", "SUBJECT-A07", "result", "stored")
	f.Add("events.appended", "SUBJECT-A07", "token", "secret")
	f.Add("events.appended", "SUBJECT-A07", "result", "bad\nline")
	f.Fuzz(func(t *testing.T, typ, subject, key, value string) {
		e := Event{ID: "EVENT-FUZZ-A07", Type: Type(typ), Subject: SubjectID(subject), IdempotencyKey: "REQ-FUZZ-A07", Data: map[string]string{key: value}}
		err := e.Validate()
		if err == nil {
			if !e.Type.Valid() || !validRequired(string(e.Subject)) || key == "" || len(key) > MaxDataKeyBytes || len(value) > MaxDataValueBytes || !validText(key) || !validText(value) || sensitiveKey(key) {
				t.Fatalf("invalid event accepted: type=%q subject=%q key=%q", typ, subject, key)
			}
		}
	})
}

func FuzzT43A07CloneEventDataDoesNotAlias(f *testing.F) {
	f.Add("value")
	f.Fuzz(func(t *testing.T, value string) {
		if !validText(value) || len(value) > MaxDataValueBytes {
			return
		}
		original := Event{Data: map[string]string{"result": value}}
		clone := CloneEvent(original)
		clone.Data["result"] = "changed"
		if original.Data["result"] != value {
			t.Fatal("CloneEvent aliased caller map")
		}
	})
}
