package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func newApprovalStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()

	st, err := Open(ctx, filepath.Join(t.TempDir(), "approvals.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := st.InitProject(ctx, model.Project{
		ID:            "proj-approval",
		Repository:    "/repo/approval",
		DefaultBranch: "main",
		PackVersion:   "6.0.0",
	}); err != nil {
		t.Fatalf("init project: %v", err)
	}
	return st, ctx
}

func requestApproval(t *testing.T, st *Store, ctx context.Context, id string) {
	t.Helper()
	if err := st.CreateApproval(ctx, model.Approval{
		ID:          id,
		ProjectID:   "proj-approval",
		Operation:   model.Operation("deploy"),
		Scope:       "release",
		Target:      "production",
		RequestedBy: "codex",
		Status:      model.ApprovalRequested,
		CreatedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create approval %s: %v", id, err)
	}
}

func TestListPendingApprovalsReturnsOnlyRequested(t *testing.T) {
	st, ctx := newApprovalStore(t)
	requestApproval(t, st, ctx, "a-1")
	requestApproval(t, st, ctx, "a-2")

	pending, err := st.ListPendingApprovals(ctx, "proj-approval")
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending, got %d", len(pending))
	}

	expiry := time.Now().UTC().Add(time.Hour)
	if _, err := st.ResolveApproval(ctx, "a-1", "operator", true, &expiry, 0); err != nil {
		t.Fatalf("resolve a-1: %v", err)
	}

	pending, err = st.ListPendingApprovals(ctx, "proj-approval")
	if err != nil {
		t.Fatalf("list pending after resolve: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "a-2" {
		t.Fatalf("expected only a-2 pending, got %+v", pending)
	}
}

func TestResolveApprovalIsConcurrencySafe(t *testing.T) {
	st, ctx := newApprovalStore(t)
	requestApproval(t, st, ctx, "a-cas")

	expiry := time.Now().UTC().Add(time.Hour)
	if _, err := st.ResolveApproval(ctx, "a-cas", "operator", true, &expiry, 0); err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	// A second operator holding the stale revision must lose, not overwrite.
	_, err := st.ResolveApproval(ctx, "a-cas", "other", false, nil, 0)
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("expected ErrConflict on stale revision, got %v", err)
	}

	stored, err := st.GetApproval(ctx, "a-cas")
	if err != nil {
		t.Fatalf("get approval: %v", err)
	}
	if stored.Status != model.ApprovalApproved || stored.ApprovedBy != "operator" {
		t.Fatalf("first decision must stand, got %s by %q", stored.Status, stored.ApprovedBy)
	}
}

func TestResolveApprovalRequiresExpiryWhenApproving(t *testing.T) {
	st, ctx := newApprovalStore(t)
	requestApproval(t, st, ctx, "a-noexpiry")

	if _, err := st.ResolveApproval(ctx, "a-noexpiry", "operator", true, nil, 0); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("expected ErrInvalid without expiry, got %v", err)
	}

	// A denial legitimately has no expiry.
	denied, err := st.ResolveApproval(ctx, "a-noexpiry", "operator", false, nil, 0)
	if err != nil {
		t.Fatalf("deny: %v", err)
	}
	if denied.Status != model.ApprovalDenied || denied.ExpiresAt != nil {
		t.Fatalf("expected denial without expiry, got %s / %v", denied.Status, denied.ExpiresAt)
	}
}

func TestGetApprovalReportsMissingRecord(t *testing.T) {
	st, ctx := newApprovalStore(t)
	if _, err := st.GetApproval(ctx, "absent"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// TestApprovedRecordValidatesThroughPolicyPath proves a TUI-granted approval is
// accepted by the existing ValidateApproval gate, so /approve feeds the real
// policy mechanism rather than a parallel one.
func TestApprovedRecordValidatesThroughPolicyPath(t *testing.T) {
	st, ctx := newApprovalStore(t)
	requestApproval(t, st, ctx, "a-policy")

	expiry := time.Now().UTC().Add(time.Hour)
	resolved, err := st.ResolveApproval(ctx, "a-policy", "operator", true, &expiry, 0)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if _, err := st.ValidateApproval(ctx, model.ApprovalUse{
		ID:               "a-policy",
		Operation:        model.Operation("deploy"),
		Scope:            "release",
		Target:           "production",
		Now:              time.Now().UTC(),
		ExpectedRevision: resolved.Revision,
	}); err != nil {
		t.Fatalf("granted approval must satisfy ValidateApproval: %v", err)
	}
}
