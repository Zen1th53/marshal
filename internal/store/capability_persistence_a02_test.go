package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestCapabilityGrantPersistenceRoundTripAndIdempotency(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 16, 13, 0, 0, 0, time.UTC)
	grant := capability.Grant{
		ID: "CAP-A02-1", Subject: "agent-1", TaskID: "task-1", Kind: capability.KindFilesystemWrite,
		Scope: capability.Scope{
			Resource:    "/workspace/task-1",
			Actions:     []string{"write"},
			Constraints: map[string]string{"worktree": "/workspace/task-1"},
		},
		IssuedAt: now, ExpiresAt: now.Add(time.Hour), Issuer: "broker",
		PolicyDigest: "sha256:" + strings.Repeat("a", 64),
	}
	if err := st.PutCapabilityGrant(ctx, grant); err != nil {
		t.Fatalf("PutCapabilityGrant: %v", err)
	}
	if err := st.PutCapabilityGrant(ctx, grant); err != nil {
		t.Fatalf("idempotent PutCapabilityGrant: %v", err)
	}

	loaded, err := st.GetCapabilityGrant(ctx, grant.ID)
	if err != nil {
		t.Fatalf("GetCapabilityGrant: %v", err)
	}
	if loaded.ID != grant.ID || loaded.Subject != grant.Subject || loaded.TaskID != grant.TaskID ||
		loaded.Kind != grant.Kind || loaded.Scope.String() != grant.Scope.String() ||
		loaded.PolicyDigest != grant.PolicyDigest || !loaded.ExpiresAt.Equal(grant.ExpiresAt) {
		t.Fatalf("loaded grant mismatch: %#v", loaded)
	}

	conflict := grant
	conflict.Scope.Resource = "/other"
	if err := st.PutCapabilityGrant(ctx, conflict); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("conflicting grant error = %v, want model conflict", err)
	}
}

func TestCapabilityGrantPersistenceReopensAndRevokesWithCAS(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/state.db"
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 16, 13, 0, 0, 0, time.UTC)
	grant := capability.Grant{
		ID: "CAP-A02-2", Subject: "agent-2", TaskID: "task-2", Kind: capability.KindGitPush,
		Scope:    capability.Scope{Resource: "repo-2", Actions: []string{"push"}},
		IssuedAt: now, ExpiresAt: now.Add(time.Hour), Issuer: "broker",
	}
	if err := first.PutCapabilityGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	revokedAt := now.Add(10 * time.Minute)
	if err := second.RevokeCapabilityGrant(ctx, grant.ID, revokedAt); err != nil {
		t.Fatalf("RevokeCapabilityGrant: %v", err)
	}
	loaded, err := second.GetCapabilityGrant(ctx, grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RevokedAt == nil || !loaded.RevokedAt.Equal(revokedAt) {
		t.Fatalf("revoked_at = %v, want %v", loaded.RevokedAt, revokedAt)
	}
	if err := second.RevokeCapabilityGrant(ctx, grant.ID, revokedAt); err == nil {
		t.Fatal("second revoke unexpectedly succeeded")
	}
}

func TestCapabilityGrantRejectsInvalidDigestAndPreIssueRevoke(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 16, 13, 0, 0, 0, time.UTC)
	grant := capability.Grant{
		ID: "CAP-A02-3", Subject: "agent-3", TaskID: "task-3", Kind: capability.KindSecretUse,
		Scope:    capability.Scope{Resource: "secret://task-3", Actions: []string{"use"}},
		IssuedAt: now, ExpiresAt: now.Add(time.Hour), Issuer: "broker",
		PolicyDigest: "MARSHAL_TEST_SECRET_T01_A02_invalid_digest",
	}
	if err := st.PutCapabilityGrant(ctx, grant); err == nil || strings.Contains(err.Error(), "MARSHAL_TEST_SECRET_T01_A02") {
		t.Fatalf("invalid digest result = %v", err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM capability_grants"); got != 0 {
		t.Fatalf("invalid grant rows = %d, want 0", got)
	}

	grant.PolicyDigest = ""
	if err := st.PutCapabilityGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeCapabilityGrant(ctx, grant.ID, now.Add(-time.Minute)); err == nil {
		t.Fatal("pre-issue revoke unexpectedly succeeded")
	}
	loaded, err := st.GetCapabilityGrant(ctx, grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RevokedAt != nil {
		t.Fatal("invalid pre-issue revoke mutated durable state")
	}
	if _, err := st.db.ExecContext(ctx, "UPDATE capability_grants SET policy_digest = ? WHERE id = ?", "malformed", grant.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetCapabilityGrant(ctx, grant.ID); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("corrupt digest error = %v, want model invalid", err)
	}
}
