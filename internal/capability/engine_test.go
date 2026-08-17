package capability

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type memoryGrantRepository struct {
	grants map[GrantID]Grant
}

type testAuthority struct{}

func (testAuthority) AuthorizeGrant(context.Context, GrantRequest) error          { return nil }
func (testAuthority) AuthorizeRevoke(context.Context, RevokeRequest, Grant) error { return nil }

func (r *memoryGrantRepository) PutCapabilityGrant(_ context.Context, grant Grant) error {
	if existing, ok := r.grants[grant.ID]; ok && !reflect.DeepEqual(existing, grant) {
		return ErrDenied
	}
	r.grants[grant.ID] = grant
	return nil
}

func (r *memoryGrantRepository) GetCapabilityGrant(_ context.Context, id GrantID) (Grant, error) {
	grant, ok := r.grants[id]
	if !ok {
		return Grant{}, ErrDenied
	}
	return grant, nil
}

func (r *memoryGrantRepository) FindCapabilityGrantByIdempotencyKey(_ context.Context, key string) (Grant, bool, error) {
	for _, grant := range r.grants {
		if grant.IdempotencyKey == key {
			return grant, true, nil
		}
	}
	return Grant{}, false, nil
}

func (r *memoryGrantRepository) ListCapabilityGrants(_ context.Context) ([]Grant, error) {
	result := make([]Grant, 0, len(r.grants))
	for _, grant := range r.grants {
		result = append(result, grant)
	}
	return result, nil
}

func (r *memoryGrantRepository) RevokeCapabilityGrant(_ context.Context, id GrantID, revokedAt time.Time) error {
	grant, ok := r.grants[id]
	if !ok || grant.RevokedAt != nil {
		return ErrRevoked
	}
	grant.RevokedAt = &revokedAt
	r.grants[id] = grant
	return nil
}

func TestEngineGrantsAndAuthorizesExactScopedRequest(t *testing.T) {
	now := time.Date(2026, 8, 16, 14, 0, 0, 0, time.UTC)
	repo := &memoryGrantRepository{grants: map[GrantID]Grant{}}
	engine := NewAuthorizedEngine(repo, func() time.Time { return now }, testAuthority{})
	grant, err := engine.Grant(context.Background(), GrantRequest{
		Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemWrite,
		Scope:     Scope{Resource: "/workspace/task-1", Actions: []string{"write"}},
		ExpiresAt: now.Add(time.Hour), Issuer: "broker", IdempotencyKey: "request-1",
	})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	decision, err := engine.Authorize(context.Background(), Query{
		Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemWrite,
		Resource: "/workspace/task-1", Action: "write", At: now,
	})
	if err != nil || decision.Outcome != OutcomeAllow || decision.MatchedGrant != grant.ID {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
}

func TestEngineDenyReasonsAndIdempotentGrant(t *testing.T) {
	now := time.Date(2026, 8, 16, 14, 0, 0, 0, time.UTC)
	repo := &memoryGrantRepository{grants: map[GrantID]Grant{}}
	engine := NewAuthorizedEngine(repo, func() time.Time { return now }, testAuthority{})
	request := GrantRequest{
		Subject: "agent-1", TaskID: "task-1", Kind: KindSecretUse,
		Scope:     Scope{Resource: "secret://task-1", Actions: []string{"use"}},
		ExpiresAt: now.Add(time.Hour), Issuer: "broker", IdempotencyKey: "request-2",
	}
	first, err := engine.Grant(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Grant(context.Background(), request)
	if err != nil || first.ID != second.ID {
		t.Fatalf("retry first=%#v second=%#v err=%v", first, second, err)
	}
	denied, err := engine.Authorize(context.Background(), Query{
		Subject: "agent-1", TaskID: "task-1", Kind: KindSecretUse,
		Resource: "secret://task-1", Action: "read", At: now,
	})
	if err != nil || denied.Outcome != OutcomeDeny || denied.Reason != CodeDenied {
		t.Fatalf("denied=%#v err=%v", denied, err)
	}
	if err := engine.Revoke(context.Background(), RevokeRequest{GrantID: first.ID, Actor: "broker"}); err != nil {
		t.Fatal(err)
	}
	revoked, err := engine.Authorize(context.Background(), Query{
		Subject: "agent-1", TaskID: "task-1", Kind: KindSecretUse,
		Resource: "secret://task-1", Action: "use", At: now,
	})
	if err != nil || revoked.Outcome != OutcomeDeny || revoked.Reason != CodeRevoked {
		t.Fatalf("revoked=%#v err=%v", revoked, err)
	}
	if !errors.Is(ErrRevoked, ErrRevoked) {
		t.Fatal("revoked error lost errors.Is support")
	}
}

func TestEngineRejectsMismatchExpiryAndUnauthorizedRevoke(t *testing.T) {
	now := time.Date(2026, 8, 16, 14, 0, 0, 0, time.UTC)
	repo := &memoryGrantRepository{grants: map[GrantID]Grant{}}
	engine := NewAuthorizedEngine(repo, func() time.Time { return now }, testAuthority{})
	grant, err := engine.Grant(context.Background(), GrantRequest{
		Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead,
		Scope:     Scope{Resource: "/workspace/task-1", Actions: []string{"read"}},
		ExpiresAt: now.Add(time.Hour), Issuer: "broker", IdempotencyKey: "request-3",
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]Query{
		"subject": {Subject: "other", TaskID: "task-1", Kind: grant.Kind, Resource: grant.Scope.Resource, Action: "read", At: now},
		"task":    {Subject: grant.Subject, TaskID: "other", Kind: grant.Kind, Resource: grant.Scope.Resource, Action: "read", At: now},
		"expired": {Subject: grant.Subject, TaskID: grant.TaskID, Kind: grant.Kind, Resource: grant.Scope.Resource, Action: "read", At: now.Add(2 * time.Hour)},
	} {
		decision, err := engine.Authorize(context.Background(), query)
		if err != nil || decision.Outcome != OutcomeDeny {
			t.Fatalf("%s decision=%#v err=%v", name, decision, err)
		}
		want := CodeSubjectMismatch
		if name == "task" {
			want = CodeTaskMismatch
		}
		if name == "expired" {
			want = CodeExpired
		}
		if decision.Reason != want {
			t.Errorf("%s reason=%q want %q", name, decision.Reason, want)
		}
	}
	if err := engine.Revoke(context.Background(), RevokeRequest{GrantID: grant.ID, Actor: "other"}); !errors.Is(err, ErrSubjectMismatch) {
		t.Fatalf("unauthorized revoke error=%v", err)
	}
}

func TestEngineFailsClosedWithoutGrantAuthority(t *testing.T) {
	now := time.Date(2026, 8, 16, 14, 0, 0, 0, time.UTC)
	repo := &memoryGrantRepository{grants: map[GrantID]Grant{}}
	engine := NewEngine(repo, func() time.Time { return now })
	_, err := engine.Grant(context.Background(), GrantRequest{
		Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead,
		Scope:     Scope{Resource: "/workspace/task-1", Actions: []string{"read"}},
		ExpiresAt: now.Add(time.Hour), Issuer: "agent-1", IdempotencyKey: "request-no-authority",
	})
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("unauthorized grant error=%v, want ErrDenied", err)
	}
	if len(repo.grants) != 0 {
		t.Fatalf("unauthorized grant mutated repository: %d rows", len(repo.grants))
	}
}

func TestEngineNormalizesFilesystemResourcesBeforeComparison(t *testing.T) {
	now := time.Date(2026, 8, 16, 14, 0, 0, 0, time.UTC)
	repo := &memoryGrantRepository{grants: map[GrantID]Grant{}}
	engine := NewAuthorizedEngine(repo, func() time.Time { return now }, testAuthority{})
	grant, err := engine.Grant(context.Background(), GrantRequest{
		Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead,
		Scope:     Scope{Resource: "/workspace/task-1/../task-1", Actions: []string{"read"}},
		ExpiresAt: now.Add(time.Hour), Issuer: "broker", IdempotencyKey: "request-normalize",
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.Scope.Resource != "/workspace/task-1" {
		t.Fatalf("normalized grant resource=%q", grant.Scope.Resource)
	}
	decision, err := engine.Authorize(context.Background(), Query{
		Subject: "agent-1", TaskID: "task-1", Kind: KindFilesystemRead,
		Resource: "/workspace/task-1/./", Action: "read", At: now,
	})
	if err != nil || decision.Outcome != OutcomeAllow {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
	if _, err := NormalizeResource(KindFilesystemRead, "../../outside"); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("path escape error=%v", err)
	}
}
