package capability

import (
	"context"
	"testing"
	"time"
)

func TestEngineGrantAuthorizeAndRevokeUsesExplicitState(t *testing.T) {
	repo := newMemoryRepository()
	engine := NewEngine(repo, func() time.Time { return time.Unix(100, 0).UTC() })
	ctx := context.Background()

	grant, err := engine.Grant(ctx, GrantRequest{
		Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead,
		Scope: Scope{Resource: "/workspace", Actions: []string{"read"}},
		TTL:   time.Hour, Issuer: "admin-1",
	})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if grant.State != GrantActive {
		t.Fatalf("state = %q, want active", grant.State)
	}

	decision, err := engine.Authorize(ctx, Query{Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead, Resource: "/workspace", Action: "read"})
	if err != nil || !decision.Allowed || decision.GrantID != grant.ID {
		t.Fatalf("Authorize = %#v, err=%v", decision, err)
	}
	if err := engine.Revoke(ctx, grant.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	decision, err = engine.Authorize(ctx, Query{Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead, Resource: "/workspace", Action: "read"})
	if err != nil || decision.Allowed || decision.Reason != ReasonRevoked {
		t.Fatalf("revoked Authorize = %#v, err=%v", decision, err)
	}
}

func TestEngineDeniesSubjectAndTaskMismatch(t *testing.T) {
	repo := newMemoryRepository()
	engine := NewEngine(repo, func() time.Time { return time.Unix(100, 0).UTC() })
	ctx := context.Background()
	if _, err := engine.Grant(ctx, GrantRequest{Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead, Scope: Scope{Resource: "/workspace", Actions: []string{"read"}}, TTL: time.Hour, Issuer: "admin-1"}); err != nil {
		t.Fatal(err)
	}
	decision, err := engine.Authorize(ctx, Query{Subject: "agent-2", TaskID: "task-1", Kind: KindFilesystemRead, Resource: "/workspace", Action: "read"})
	if err != nil || decision.Allowed || decision.Reason != ReasonSubjectMismatch {
		t.Fatalf("subject mismatch = %#v, err=%v", decision, err)
	}
	decision, err = engine.Authorize(ctx, Query{Subject: "agent-1", TaskID: "task-2", Kind: KindFilesystemRead, Resource: "/workspace", Action: "read"})
	if err != nil || decision.Allowed || decision.Reason != ReasonTaskMismatch {
		t.Fatalf("task mismatch = %#v, err=%v", decision, err)
	}
}

type memoryRepository struct{ grants map[string]Grant }

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{grants: make(map[string]Grant)}
}
func (r *memoryRepository) SaveGrant(_ context.Context, grant Grant) error {
	r.grants[grant.ID] = grant
	return nil
}
func (r *memoryRepository) LoadGrant(_ context.Context, id string) (Grant, error) {
	grant, ok := r.grants[id]
	if !ok {
		return Grant{}, ErrGrantNotFound
	}
	return grant, nil
}
func (r *memoryRepository) ListGrants(_ context.Context, kind Kind) ([]Grant, error) {
	result := make([]Grant, 0)
	for _, grant := range r.grants {
		if grant.Kind == kind {
			result = append(result, grant)
		}
	}
	return result, nil
}
func (r *memoryRepository) RevokeGrant(_ context.Context, id string, at time.Time) error {
	grant, ok := r.grants[id]
	if !ok {
		return ErrGrantNotFound
	}
	grant.State, grant.RevokedAt = GrantRevoked, &at
	r.grants[id] = grant
	return nil
}
