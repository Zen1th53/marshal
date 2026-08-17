package capability

import (
	"context"
	"testing"
	"time"
)

type auditRecorder struct{ events []AuditEvent }

func (r *auditRecorder) AppendCapabilityEvent(_ context.Context, event AuditEvent) error {
	r.events = append(r.events, event)
	return nil
}

func TestEngineEmitsSecretSafeCapabilityAuditEvents(t *testing.T) {
	store := &memoryGrantRepository{grants: map[GrantID]Grant{}}
	audit := &auditRecorder{}
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	engine := NewAuthorizedEngineWithAudit(store, func() time.Time { return now }, testAuthority{}, audit)
	grant, err := engine.Grant(context.Background(), GrantRequest{Subject: "agent", TaskID: "task", Kind: KindFilesystemWrite, Scope: Scope{Resource: "/workspace", Actions: []string{"write"}}, IssuedAt: now, ExpiresAt: now.Add(time.Hour), Issuer: "admin", IdempotencyKey: "a05-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Authorize(context.Background(), Query{Subject: "agent", TaskID: "task", Kind: KindFilesystemWrite, Resource: "/workspace", Action: "write", At: now}); err != nil {
		t.Fatal(err)
	}
	if len(audit.events) != 2 || audit.events[0].Subject != "agent" || audit.events[0].TaskID != "task" || audit.events[0].GrantID != grant.ID {
		t.Fatalf("audit events = %#v", audit.events)
	}
	for _, event := range audit.events {
		if event.Resource == "MARSHAL_TEST_SECRET_T01_A05" || event.Reason == ErrorCode("MARSHAL_TEST_SECRET_T01_A05") {
			t.Fatal("secret marker leaked into capability audit event")
		}
	}
}
