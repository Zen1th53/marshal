package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
)

func TestListEventsIncludesStoredEgress(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	event := events.Event{ID: "egress-regression", Type: events.EventTypeNetworkEgressAttempt, At: time.Now().UTC(), Subject: "worker", TaskID: "task", RunID: "run", Data: map[string]any{"reason": "NET_BROKER_SANDBOX_REFRESH_DENIED", "host": "api.anthropic.com", "rule_id": "refresh-grant-json"}}
	if _, err := st.Append(ctx, event); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(ctx, events.Event{ID: "unrelated-structured", Type: events.EventTypeTaskCreated, At: event.At, Data: map[string]any{"task_id": "another-task"}}); err != nil {
		t.Fatal(err)
	}
	got, err := st.ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != event.ID || string(got[0].Type) != string(event.Type) || got[0].TaskID != event.TaskID || got[0].ActorAgentID != event.Subject || got[0].SessionID != event.RunID || got[0].Data["reason"] != event.Data["reason"] {
		t.Fatalf("missing egress metadata: %+v", got)
	}
	// Legacy history must remain ordered and must not duplicate an event that
	// was projected into both stores.
	for _, id := range []string{"legacy-regression", event.ID} {
		if _, err := st.db.ExecContext(ctx, `INSERT INTO audit_events(event_id,event_type,aggregate_revision,timestamp,data_json) VALUES(?,?,0,?,'{}')`, id, "legacy", event.At.Add(-time.Second).Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	merged, err := st.ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 2 || merged[0].ID != event.ID || merged[1].ID != "legacy-regression" {
		t.Fatalf("legacy history or deduplication lost: %+v", merged)
	}

}
