package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestApprovalValidationBindsFullContext(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	base := model.Approval{
		ID: "APR-001", ProjectID: "PROJECT-local", Operation: model.Deploy,
		Scope: "repository", Target: "local/test", Commit: "abc123",
		RequestedBy: "AGENT-dev", ApprovedBy: "operator", Status: model.ApprovalApproved,
		CreatedAt: now.Add(-time.Hour), ExpiresAt: ptrTime(now.Add(time.Hour)),
	}
	tests := []struct {
		name   string
		mutate func(*model.ApprovalUse)
	}{
		{name: "wrong operation", mutate: func(use *model.ApprovalUse) { use.Operation = model.DestructiveOperation }},
		{name: "wrong scope", mutate: func(use *model.ApprovalUse) { use.Scope = "other" }},
		{name: "wrong target", mutate: func(use *model.ApprovalUse) { use.Target = "other" }},
		{name: "wrong commit", mutate: func(use *model.ApprovalUse) { use.Commit = "def456" }},
		{name: "expired", mutate: func(use *model.ApprovalUse) { use.Now = now.Add(2 * time.Hour) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := projectStore(t)
			if err := st.CreateApproval(context.Background(), base); err != nil {
				t.Fatal(err)
			}
			use := model.ApprovalUse{
				ID: base.ID, Operation: base.Operation, Scope: base.Scope, Target: base.Target,
				Commit: base.Commit, Now: now, ExpectedRevision: 0,
			}
			tt.mutate(&use)
			if _, err := st.ValidateApproval(context.Background(), use); !errors.Is(err, model.ErrApprovalRequired) {
				t.Fatalf("error = %v, want approval required", err)
			}
		})
	}
}

func TestApprovalConsumedRevokedAndStaleRevisionFail(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	for _, status := range []model.ApprovalStatus{model.ApprovalConsumed, model.ApprovalRevoked} {
		t.Run(string(status), func(t *testing.T) {
			st := projectStore(t)
			approval := validApproval(now)
			approval.Status = status
			if err := st.CreateApproval(context.Background(), approval); err != nil {
				t.Fatal(err)
			}
			if _, err := st.ValidateApproval(context.Background(), approvalUse(approval, now)); !errors.Is(err, model.ErrApprovalRequired) {
				t.Fatalf("error = %v", err)
			}
		})
	}

	st := projectStore(t)
	approval := validApproval(now)
	if err := st.CreateApproval(context.Background(), approval); err != nil {
		t.Fatal(err)
	}
	if err := st.ConsumeApproval(context.Background(), approval.ID, 1); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale consume error = %v", err)
	}
	if err := st.ConsumeApproval(context.Background(), approval.ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ValidateApproval(context.Background(), approvalUse(approval, now)); !errors.Is(err, model.ErrApprovalRequired) {
		t.Fatalf("consumed validation error = %v", err)
	}
}

func TestDeveloperCannotCloseQAOrAppSecFinding(t *testing.T) {
	for _, owner := range []model.Role{model.RoleQA, model.RoleAppSec} {
		t.Run(string(owner), func(t *testing.T) {
			st := projectStore(t)
			finding := model.Finding{
				ID: "FIND-" + string(owner), ProjectID: "PROJECT-local", OwnerRole: owner,
				Severity: "HIGH", Status: model.FindingOpen, Title: "owned finding",
			}
			if err := st.CreateFinding(context.Background(), finding); err != nil {
				t.Fatal(err)
			}
			err := st.TransitionFinding(context.Background(), model.FindingTransition{
				ID: finding.ID, ActorRole: model.RoleDeveloper, Status: model.FindingClosed, ExpectedRevision: 0,
			})
			if !errors.Is(err, model.ErrPolicyDenied) {
				t.Fatalf("developer close error = %v", err)
			}
			if err := st.TransitionFinding(context.Background(), model.FindingTransition{
				ID: finding.ID, ActorRole: model.RoleDeveloper, Status: model.FindingReadyForRetest, ExpectedRevision: 0,
			}); err != nil {
				t.Fatalf("developer ready-for-retest: %v", err)
			}
			if err := st.TransitionFinding(context.Background(), model.FindingTransition{
				ID: finding.ID, ActorRole: owner, Status: model.FindingClosed, ExpectedRevision: 1,
			}); err != nil {
				t.Fatalf("owner close: %v", err)
			}
		})
	}
}

func TestGeneralMemoryRejectsSecretsButAcceptsDigests(t *testing.T) {
	st := projectStore(t)
	privateKeyHeader := strings.Join([]string{"-----BEGIN", "PRIVATE", "KEY-----"}, " ")
	secretAssignment := strings.Join([]string{"api", "key"}, "_") + "=" + strings.Join([]string{"TEST", "ONLY", "VALUE"}, "_")
	for i, body := range []string{privateKeyHeader, secretAssignment} {
		record := memoryRecord("MEM-secret-"+string(rune('a'+i)), body)
		if err := st.Remember(context.Background(), record); !errors.Is(err, model.ErrSecretMaterial) {
			t.Fatalf("secret %d error = %v", i, err)
		}
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	if err := st.Remember(context.Background(), memoryRecord("MEM-digest", digest)); err != nil {
		t.Fatalf("digest rejected: %v", err)
	}
}

func validApproval(now time.Time) model.Approval {
	return model.Approval{
		ID: "APR-valid", ProjectID: "PROJECT-local", Operation: model.Deploy,
		Scope: "repository", Target: "local/test", Commit: "abc123",
		RequestedBy: "AGENT-dev", ApprovedBy: "operator", Status: model.ApprovalApproved,
		CreatedAt: now.Add(-time.Hour), ExpiresAt: ptrTime(now.Add(time.Hour)),
	}
}

func approvalUse(approval model.Approval, now time.Time) model.ApprovalUse {
	return model.ApprovalUse{
		ID: approval.ID, Operation: approval.Operation, Scope: approval.Scope,
		Target: approval.Target, Commit: approval.Commit, Now: now, ExpectedRevision: approval.Revision,
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}

func memoryRecord(id, body string) model.MemoryRecord {
	now := time.Now().UTC()
	return model.MemoryRecord{
		ID: id, ProjectID: "PROJECT-local", Type: "working", Status: "active",
		Confidence: "observed", Body: body, Provenance: map[string]any{"source": "test"},
		CreatedAt: now, UpdatedAt: now,
	}
}
