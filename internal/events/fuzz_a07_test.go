package events

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func baseA07Event() Event {
	return Event{ID: "EVENT-A07", Type: "events.appended", Subject: "system", At: time.Unix(1, 0).UTC(), Data: map[string]string{"result": "ok"}, IdempotencyKey: "REQ-A07"}
}

func TestT43A07AttackMatrixRejectsMalformedEvents(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Event)
	}{
		{"empty-id", func(e *Event) { e.ID = "" }}, {"id-control", func(e *Event) { e.ID = "EVENT\nX" }},
		{"empty-type", func(e *Event) { e.Type = "" }}, {"unknown-type", func(e *Event) { e.Type = "provider.trusted" }},
		{"empty-subject", func(e *Event) { e.Subject = "" }}, {"subject-control", func(e *Event) { e.Subject = "sys\nadmin" }},
		{"empty-idempotency", func(e *Event) { e.IdempotencyKey = "" }}, {"idem-control", func(e *Event) { e.IdempotencyKey = "REQ\tX" }},
		{"task-control", func(e *Event) { e.TaskID = "TASK\nX" }}, {"run-control", func(e *Event) { e.RunID = "RUN\rX" }},
		{"resource-control", func(e *Event) { e.ResourceID = "RES\tX" }}, {"evidence-control", func(e *Event) { e.EvidenceID = "EVID\nX" }},
		{"time-local", func(e *Event) { e.At = time.Unix(1, 0).In(time.FixedZone("x", 3600)) }},
		{"secret", func(e *Event) { e.Data = map[string]string{"secret": "x"} }}, {"token", func(e *Event) { e.Data = map[string]string{"token": "x"} }},
		{"password", func(e *Event) { e.Data = map[string]string{"password": "x"} }}, {"authorization", func(e *Event) { e.Data = map[string]string{"authorization": "x"} }},
		{"private-key", func(e *Event) { e.Data = map[string]string{"private_key": "x"} }}, {"secret-case", func(e *Event) { e.Data = map[string]string{" SeCrEt ": "x"} }},
		{"empty-data-key", func(e *Event) { e.Data = map[string]string{"": "x"} }}, {"data-key-control", func(e *Event) { e.Data = map[string]string{"a\nb": "x"} }},
		{"data-value-control", func(e *Event) { e.Data = map[string]string{"result": "x\ny"} }}, {"oversize-key", func(e *Event) { e.Data = map[string]string{strings.Repeat("k", MaxDataKeyBytes+1): "x"} }},
		{"oversize-value", func(e *Event) { e.Data = map[string]string{"result": strings.Repeat("v", MaxDataValueBytes+1)} }},
		{"pass-no-evidence-1", func(e *Event) { e.Type = "evaluation.stage.passed"; e.EvidenceID = "" }},
		{"pass-no-evidence-2", func(e *Event) { e.Type = "policytest.case.passed"; e.EvidenceID = "" }},
		{"pass-no-evidence-3", func(e *Event) { e.Type = "conformance.scenario.passed"; e.EvidenceID = "" }},
		{"pass-no-evidence-4", func(e *Event) { e.Type = "vibe.passed"; e.EvidenceID = "" }},
		{"pass-no-evidence-5", func(e *Event) { e.Type = "vibe.stage.passed"; e.EvidenceID = "" }},
		{"id-invalid-utf8", func(e *Event) { e.ID = EventID(string([]byte{'E', 0xff})) }},
		{"subject-invalid-utf8", func(e *Event) { e.Subject = SubjectID(string([]byte{'S', 0xfe})) }},
		{"idem-invalid-utf8", func(e *Event) { e.IdempotencyKey = IdempotencyKey(string([]byte{'R', 0xfd})) }},
		{"task-invalid-utf8", func(e *Event) { e.TaskID = TaskID(string([]byte{'T', 0xfc})) }},
		{"run-invalid-utf8", func(e *Event) { e.RunID = RunID(string([]byte{'R', 0xfb})) }},
		{"resource-invalid-utf8", func(e *Event) { e.ResourceID = ResourceID(string([]byte{'X', 0xfa})) }},
		{"evidence-invalid-utf8", func(e *Event) { e.EvidenceID = EvidenceID(string([]byte{'E', 0xf9})) }},
	}
	if len(cases) < 30 {
		t.Fatalf("attack matrix too small: %d", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := baseA07Event()
			tc.mutate(&e)
			if err := e.Validate(); err == nil {
				t.Fatalf("malformed event accepted: %+v", e)
			}
		})
	}
}

func FuzzT43A07EventValidationNeverAcceptsUnsafeText(f *testing.F) {
	f.Add("EVENT-A", "system", "REQ-A", "result", "ok")
	f.Add("EVENT-A\n", "system", "REQ-A", "result", "ok")
	f.Add("EVENT-A", "system", "REQ-A", "secret", "marker")
	f.Fuzz(func(t *testing.T, id, subject, idem, key, value string) {
		e := baseA07Event()
		e.ID = EventID(id)
		e.Subject = SubjectID(subject)
		e.IdempotencyKey = IdempotencyKey(idem)
		e.Data = map[string]string{key: value}
		err := e.Validate()
		unsafe := !validRequired(id) || !validRequired(subject) || !validRequired(idem) || key == "" || len(key) > MaxDataKeyBytes || len(value) > MaxDataValueBytes || !validText(key) || !validText(value) || sensitiveKey(key)
		if unsafe && err == nil {
			t.Fatalf("unsafe event accepted")
		}
		if !unsafe && err != nil && !errors.Is(err, ErrEvidenceRequired) {
			t.Fatalf("safe event rejected: %v", err)
		}
	})
}
