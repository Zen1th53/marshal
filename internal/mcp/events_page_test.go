package mcp

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestPageEventsCursorFilterLimit(t *testing.T) {
	base := time.Unix(100, 0)
	events := []model.Event{
		{ID: "E1", Type: "a", TaskID: "T1", Timestamp: base},
		{ID: "E2", Type: "b", TaskID: "T2", Timestamp: base},
		{ID: "E3", Type: "a", TaskID: "T2", Timestamp: base.Add(time.Second)},
		{ID: "E4", Type: "a", TaskID: "T1", Timestamp: base.Add(2 * time.Second)},
	}
	if _, paged, _ := pageEvents(events, map[string]any{}); paged {
		t.Fatal("no arguments must keep the legacy full array")
	}
	page, _, err := pageEvents(events, map[string]any{"limit": float64(2)})
	if err != nil || len(page.Events) != 2 || !page.HasMore || page.Events[1].ID != "E2" {
		t.Fatalf("limit page %+v %v", page, err)
	}
	next, _, err := pageEvents(events, map[string]any{"since_cursor": page.NextCursor, "types": "a"})
	if err != nil || len(next.Events) != 2 || next.Events[0].ID != "E3" || next.HasMore {
		t.Fatalf("cursor page %+v %v", next, err)
	}
	empty, _, err := pageEvents(events, map[string]any{"since_cursor": next.NextCursor})
	if err != nil || len(empty.Events) != 0 || empty.NextCursor != next.NextCursor {
		t.Fatalf("tail page %+v %v", empty, err)
	}
	byTask, _, _ := pageEvents(events, map[string]any{"task_id": "T1"})
	if len(byTask.Events) != 2 || byTask.Events[1].ID != "E4" {
		t.Fatalf("task filter %+v", byTask)
	}
	for _, bad := range []map[string]any{{"since_cursor": "x"}, {"limit": float64(0)}, {"limit": "5"}} {
		if _, _, err := pageEvents(events, bad); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
}
