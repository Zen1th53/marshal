package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
)

func TestCapabilityAuditEventPersistsWithoutSecretMaterial(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	err := st.AppendCapabilityEvent(ctx, capability.AuditEvent{ID: "capability.test.a05", Type: "capability.denied", GrantID: "cap-1", Subject: "agent-1", TaskID: "task-1", Kind: capability.KindSecretUse, Resource: "secret://task-1", Reason: capability.CodeDenied, Timestamp: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	var data string
	if err := st.db.QueryRowContext(ctx, "SELECT data_json FROM audit_events WHERE event_id = ?", "capability.test.a05").Scan(&data); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(data, "MARSHAL_TEST_SECRET_T01_A05") {
		t.Fatal("secret marker persisted in capability audit event")
	}
	if err := st.AppendCapabilityEvent(ctx, capability.AuditEvent{ID: "capability.test.a05", Type: "capability.denied", GrantID: "cap-1", Subject: "agent-1", TaskID: "task-1", Kind: capability.KindSecretUse, Resource: "secret://task-1", Reason: capability.CodeDenied, Timestamp: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatalf("idempotent audit append: %v", err)
	}
}
