package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func paddedEvents(pad int) []model.Event {
	base := time.Unix(100, 0)
	return []model.Event{
		{ID: "E1", Type: "a", Timestamp: base, Data: map[string]any{"p": "x"}},
		{ID: "E2", Type: "a", Timestamp: base.Add(time.Second), Data: map[string]any{"p": strings.Repeat("x", pad)}},
	}
}

// An events page that encodes to exactly the bound is returned whole; one
// byte more ends the page early with a cursor to the rest and a limit, and
// a single event over the bound is a named refusal that can be skipped.
func TestEventsPageLimitAtCap(t *testing.T) {
	args := map[string]any{"since_cursor": "0:E0"}
	page, _, err := pageEvents(paddedEvents(0), args)
	if err != nil {
		t.Fatal(err)
	}
	pad := eventsPageMaxBytes - encodedEventsPage(page)
	page, _, err = pageEvents(paddedEvents(pad), args)
	if err != nil || len(page.Events) != 2 || page.HasMore || page.Limit != nil || encodedEventsPage(page) != eventsPageMaxBytes {
		t.Fatalf("at bound: %d events limit %+v size %d err %v", len(page.Events), page.Limit, encodedEventsPage(page), err)
	}
	over := paddedEvents(pad + 1)
	page, _, err = pageEvents(over, args)
	if err != nil || len(page.Events) != 1 || !page.HasMore || page.Limit == nil || encodedEventsPage(page) > eventsPageMaxBytes {
		t.Fatalf("one over: %+v err %v", page.Limit, err)
	}
	if page.NextCursor != eventCursor(over[0]) || page.Limit.Cursor != page.NextCursor || page.Limit.ActualBytes != eventsPageMaxBytes+1 || page.Limit.CapBytes != eventsPageMaxBytes || page.Limit.Action == "" {
		t.Fatalf("one over limit %+v cursor %s", page.Limit, page.NextCursor)
	}
	// The second event alone still fits its own page.
	rest, _, err := pageEvents(over, map[string]any{"since_cursor": page.NextCursor})
	if err != nil || len(rest.Events) != 1 || rest.Events[0].ID != "E2" || rest.Limit != nil {
		t.Fatalf("rest %+v err %v", rest.Limit, err)
	}
	// A first event that fits alone but leaves no room for the limit is
	// still served, with has_more and its cursor.
	near := paddedEvents(0)
	near[0].Data["p"] = strings.Repeat("x", eventsPageMaxBytes-encodedEventsPage(eventsPage{Events: near[:1], NextCursor: eventCursor(near[0])})-20+1)
	page, _, err = pageEvents(near, args)
	if err != nil || len(page.Events) != 1 || page.Events[0].ID != "E1" || !page.HasMore || page.Limit != nil || page.NextCursor != eventCursor(near[0]) || encodedEventsPage(page) > eventsPageMaxBytes {
		t.Fatalf("near-full first event: %d events %+v err %v", len(page.Events), page.Limit, err)
	}
	huge := paddedEvents(eventsPageMaxBytes)
	_, _, err = pageEvents(huge, map[string]any{"since_cursor": eventCursor(huge[0])})
	limit, ok := model.LimitOf(err)
	if !ok || limit.Kind != "event" || limit.Cursor != eventCursor(huge[1]) || !strings.Contains(limit.Action, limit.Cursor) {
		t.Fatalf("huge event: %+v %v", limit, err)
	}
	wire := toolError("events_list", err)
	if wire.Data == nil || wire.Data.Code != "invalid_input" && wire.Data.Code != "limit_exceeded" || wire.Data.Limit == nil {
		raw, _ := json.Marshal(wire)
		t.Fatalf("tool error %s", raw)
	}
	skipped, _, err := pageEvents(huge, map[string]any{"since_cursor": limit.Cursor})
	if err != nil || len(skipped.Events) != 0 {
		t.Fatalf("skip %+v %v", skipped, err)
	}
}
