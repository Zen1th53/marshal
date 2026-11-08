//go:build !governed

package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestExtremeRunTimelineBoundsAndProjectsMetadataOnly(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := t.Context()
	run := marshal.Run{PlanID: "plan", PlanVersion: 1, State: marshal.Dispatching, Settings: marshal.DefaultSettings()}
	if _, err := s.SetMarshalRun(ctx, "p", "selected", run, 0); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetMarshalRun(ctx, "p", "selected")
	for i := 0; i < 35; i++ {
		if _, err := s.Append(ctx, events.Event{ID: fmt.Sprintf("event-%d", i), RunID: "selected", Subject: "p", TaskID: "task", Type: events.EventTypeMarshalTaskDispatched,
			At: time.Now().UTC(), Data: map[string]any{"private_note": "payload-must-not-be-returned"}, ResourceID: "private-resource", IdempotencyKey: fmt.Sprintf("key-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, event := range []events.Event{
		{ID: "foreign-subject", RunID: "selected", Subject: "foreign"},
		{ID: "foreign-run", RunID: "other", Subject: "p"},
	} {
		event.Type, event.At = events.EventTypeMarshalTaskDispatched, time.Now().UTC()
		if _, err := s.Append(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.RunEventTimeline(ctx, "p", "selected", 20)
	if err != nil || len(rows) != 20 || rows[0].ID != "event-34" || rows[19].ID != "event-15" {
		t.Fatalf("wrong window or foreign event leak: %+v %v", rows, err)
	}
	for _, event := range rows {
		if event.RunID != "selected" || event.Subject != "" || event.Data != nil || event.IdempotencyKey != "" || event.ResourceID != "" || event.EvidenceID != "" {
			t.Fatalf("non-metadata projection: %+v", event)
		}
	}
	for _, scope := range []struct{ project, run string }{{"foreign", "selected"}, {"p", "missing"}} {
		if _, err := s.RunEventTimeline(ctx, scope.project, scope.run, 20); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("foreign or absent scope admitted: %v", err)
		}
	}
	for _, limit := range []int{-1, 0, 21, 1000000} {
		if _, err := s.RunEventTimeline(ctx, "p", "selected", limit); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("unbounded limit %d accepted: %v", limit, err)
		}
	}
	// A malformed payload never needs decoding for this read-only projection.
	if _, err := s.db.ExecContext(ctx, "UPDATE structured_events SET data_json = ? WHERE event_id = ?", "not JSON", "event-34"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunEventTimeline(ctx, "p", "selected", 1); err != nil {
		t.Fatalf("timeline unnecessarily decoded raw payload: %v", err)
	}
	after, _ := s.GetMarshalRun(ctx, "p", "selected")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("reading timeline changed durable run")
	}
	data, _ := json.Marshal(rows)
	if len(data) > 12000 {
		t.Fatalf("unexpected unbounded metadata response: %d", len(data))
	}
}
