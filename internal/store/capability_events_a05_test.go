package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
)

func TestCapabilityEventSinkPersistsIdempotently(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	event := capability.AuditEvent{ID: "capability.authorize.denied:event-1", Type: "capability.authorize.denied", Subject: "agent-1", Kind: capability.KindFilesystemRead, Resource: "/workspace", Reason: capability.ReasonDenied, Timestamp: time.Unix(100, 0).UTC()}
	if err := st.AppendCapabilityEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendCapabilityEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM audit_events WHERE event_id = ?", event.ID); got != 1 {
		t.Fatalf("event count = %d, want 1", got)
	}
}
