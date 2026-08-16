package capability

import (
	"context"
	"testing"
	"time"
)

func TestA10AuditEventCorrelatesExactGrantWithoutRawResource(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	repo := &memoryGrantRepository{grants: map[GrantID]Grant{}}
	events := &memoryEventStore{}
	engine := NewAuditedEngine(repo, func() time.Time { return now }, testAuthority{}, events)
	grant, err := engine.Grant(context.Background(), GrantRequest{
		Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead,
		Scope:     Scope{Resource: "/workspace/release-a10", Actions: []string{"read"}},
		ExpiresAt: now.Add(time.Hour), Issuer: "broker", IdempotencyKey: "release-a10",
	})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, err := engine.Authorize(context.Background(), Query{
		Subject: grant.Subject, TaskID: grant.TaskID, Kind: grant.Kind,
		Resource: grant.Scope.Resource, Action: "read", At: now,
	}); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if len(events.items) != 3 {
		t.Fatalf("events=%d want 3", len(events.items))
	}
	authorized := events.items[2]
	if authorized.Subject != "agent-1" || authorized.TaskID != "task-1" || string(authorized.ResourceID) != resourceReference(grant.Scope.Resource) {
		t.Fatalf("authorization correlation=%#v", authorized)
	}
	if authorized.Data["grant_id"] != string(grant.ID) || authorized.Data["kind"] != string(grant.Kind) {
		t.Fatalf("authorization data=%#v", authorized.Data)
	}
	if string(authorized.ResourceID) == grant.Scope.Resource {
		t.Fatal("raw resource was used as the durable event resource identifier")
	}
}
