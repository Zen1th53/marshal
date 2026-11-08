package store

import (
	"context"
	"testing"
	"time"
)

func TestKnowledgeReadLeaseLifecycle(t *testing.T) {
	st := projectStore(t)
	project, err := st.Project(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	lease := KnowledgeReadLease{ID: "KLEASE-read", ProjectID: project.ID, ProjectIdentity: "project-identity", TaskID: "TASK-read", PrincipalID: "reader", Worktree: t.TempDir(), RuntimeID: "runtime", ExpiresAt: time.Now().UTC().Add(time.Minute)}
	if err := st.CreateKnowledgeReadLease(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	if got, err := st.KnowledgeReadLease(t.Context(), lease.ID); err != nil || got != lease {
		t.Fatalf("persisted binding: %+v %v", got, err)
	}
	if err := st.RenewKnowledgeReadLease(t.Context(), lease.ID, "other-runtime", time.Now().Add(time.Minute)); err == nil {
		t.Fatal("foreign runtime renewed lease")
	}
	if err := st.RenewKnowledgeReadLease(t.Context(), lease.ID, lease.RuntimeID, time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeKnowledgeReadLease(t.Context(), lease.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.KnowledgeReadLease(t.Context(), lease.ID); err == nil {
		t.Fatal("revoked lease returned")
	}
	if err := st.RenewKnowledgeReadLease(t.Context(), lease.ID, lease.RuntimeID, time.Now().Add(time.Minute)); err == nil {
		t.Fatal("revoked lease revived")
	}
	lease.ID = "KLEASE-expired"
	if err := st.CreateKnowledgeReadLease(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(context.Background(), "UPDATE knowledge_read_leases SET expires_at=? WHERE lease_id=?", time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), lease.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.KnowledgeReadLease(t.Context(), lease.ID); err == nil {
		t.Fatal("expired lease returned")
	}
	if err := st.RenewKnowledgeReadLease(t.Context(), lease.ID, lease.RuntimeID, time.Now().Add(time.Minute)); err == nil {
		t.Fatal("expired lease revived")
	}
	lease.ID = "KLEASE-shutdown"
	if err := st.CreateKnowledgeReadLease(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeRuntimeKnowledgeReadLeases(t.Context(), lease.RuntimeID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.KnowledgeReadLease(t.Context(), lease.ID); err == nil {
		t.Fatal("shutdown lease remained active")
	}
}
