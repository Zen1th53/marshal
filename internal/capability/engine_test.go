package capability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
)

func TestEngineGrantAuthorizeAndRevokeUsesExplicitState(t *testing.T) {
	repo := newMemoryRepository()
	engine := NewEngine(repo, func() time.Time { return time.Unix(100, 0).UTC() }, allowAuthority{})
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
	if err := engine.Revoke(ctx, RevokeRequest{ID: grant.ID, Actor: "admin-1"}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	decision, err = engine.Authorize(ctx, Query{Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead, Resource: "/workspace", Action: "read"})
	if err != nil || decision.Allowed || decision.Reason != ReasonRevoked {
		t.Fatalf("revoked Authorize = %#v, err=%v", decision, err)
	}
}

func TestEngineDeniesSubjectAndTaskMismatch(t *testing.T) {
	repo := newMemoryRepository()
	engine := NewEngine(repo, func() time.Time { return time.Unix(100, 0).UTC() }, allowAuthority{})
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

func TestEngineAuditSinkReceivesBoundedCapabilityEvents(t *testing.T) {
	repo := newMemoryRepository()
	audit := &memoryAudit{}
	engine := NewEngineWithAudit(repo, func() time.Time { return time.Unix(100, 0).UTC() }, allowAuthority{}, audit)
	grant, err := engine.Grant(context.Background(), GrantRequest{Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead, Scope: Scope{Resource: "/workspace", Actions: []string{"read"}}, TTL: time.Hour, Issuer: "admin-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Authorize(context.Background(), Query{Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead, Resource: "/workspace", Action: "read"}); err != nil {
		t.Fatal(err)
	}
	if len(audit.events) != 2 || audit.events[0].Type != "capability.grant.issued" || audit.events[1].Type != "capability.authorize.allowed" || audit.events[0].GrantID != grant.ID {
		t.Fatalf("events = %#v", audit.events)
	}
	if audit.events[0].Resource != "/workspace" || audit.events[0].Timestamp.IsZero() {
		t.Fatalf("event metadata = %#v", audit.events[0])
	}
}

func TestEngineAuditSinkReceivesDeniedDecision(t *testing.T) {
	audit := &memoryAudit{}
	engine := NewEngineWithAudit(newMemoryRepository(), func() time.Time { return time.Unix(100, 0).UTC() }, allowAuthority{}, audit)
	decision, err := engine.Authorize(context.Background(), Query{Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead, Resource: "/workspace", Action: "read"})
	if err != nil || decision.Allowed || decision.Reason != ReasonSubjectMismatch {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
	if len(audit.events) != 1 || audit.events[0].Type != "capability.authorize.denied" {
		t.Fatalf("events=%#v", audit.events)
	}
}

func TestEngineMetricsRecordAllowAndDeniedWithoutIdentifiers(t *testing.T) {
	metrics := evidence.NewMetricsRecorder()
	engine := NewEngineWithObservability(newMemoryRepository(), func() time.Time { return time.Unix(100, 0).UTC() }, allowAuthority{}, nil, metrics)
	if _, err := engine.Grant(context.Background(), GrantRequest{Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead, Scope: Scope{Resource: "/workspace", Actions: []string{"read"}}, TTL: time.Hour, Issuer: "admin"}); err != nil {
		t.Fatal(err)
	}
	_, _ = engine.Authorize(context.Background(), Query{Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead, Resource: "/workspace", Action: "read"})
	_, _ = engine.Authorize(context.Background(), Query{Subject: "agent-2", TaskID: "task-1", Kind: KindFilesystemRead, Resource: "/workspace", Action: "read"})
	snapshot := metrics.Snapshot()
	if snapshot.Observations[evidence.MetricOperationCapability] != 2 || snapshot.Success[evidence.MetricOperationCapability] != 1 || len(snapshot.Denied) != 1 {
		t.Fatalf("metrics = %#v", snapshot)
	}
}

func TestEngineRejectsGrantWithoutSeparateAuthorityBeforeMutation(t *testing.T) {
	repo := newMemoryRepository()
	engine := NewEngine(repo, func() time.Time { return time.Unix(100, 0).UTC() }, nil)
	_, err := engine.Grant(context.Background(), GrantRequest{Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead, Scope: Scope{Resource: "/workspace", Actions: []string{"read"}}, TTL: time.Hour, Issuer: "agent-1"})
	if !errors.Is(err, ErrDenied) || len(repo.grants) != 0 {
		t.Fatalf("grant err=%v rows=%d, want denied and zero mutation", err, len(repo.grants))
	}
}

type allowAuthority struct{}

func (allowAuthority) AuthorizeGrant(context.Context, GrantRequest) error   { return nil }
func (allowAuthority) AuthorizeRevoke(context.Context, RevokeRequest) error { return nil }

type memoryAudit struct{ events []AuditEvent }

func (a *memoryAudit) AppendCapabilityEvent(_ context.Context, event AuditEvent) error {
	a.events = append(a.events, event)
	return nil
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
