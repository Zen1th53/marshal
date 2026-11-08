package store

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// KnowledgeReadLease grants only source metadata reads for one provider invocation.
// These leases do not claim tasks or authorize code, policy or memory mutations.
type KnowledgeReadLease struct {
	ID, ProjectID, ProjectIdentity, TaskID, PrincipalID, Worktree, RuntimeID string
	ExpiresAt                                                                time.Time
}

func (s *Store) CreateKnowledgeReadLease(ctx context.Context, l KnowledgeReadLease) error {
	if l.ID == "" || l.ProjectID == "" || l.ProjectIdentity == "" || l.TaskID == "" || l.PrincipalID == "" || l.Worktree == "" || l.RuntimeID == "" || !l.ExpiresAt.After(time.Now().UTC()) {
		return model.ErrInvalid
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO knowledge_read_leases(lease_id,project_id,project_identity,task_id,principal_id,worktree,runtime_id,expires_at) VALUES(?,?,?,?,?,?,?,?)`, l.ID, l.ProjectID, l.ProjectIdentity, l.TaskID, l.PrincipalID, l.Worktree, l.RuntimeID, l.ExpiresAt.UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) KnowledgeReadLease(ctx context.Context, id string) (KnowledgeReadLease, error) {
	var l KnowledgeReadLease
	var expiry string
	err := s.db.QueryRowContext(ctx, `SELECT lease_id,project_id,project_identity,task_id,principal_id,worktree,runtime_id,expires_at FROM knowledge_read_leases WHERE lease_id=? AND revoked=0`, id).Scan(&l.ID, &l.ProjectID, &l.ProjectIdentity, &l.TaskID, &l.PrincipalID, &l.Worktree, &l.RuntimeID, &expiry)
	if err != nil {
		return l, err
	}
	l.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiry)
	if err != nil {
		return l, err
	}
	if !l.ExpiresAt.After(time.Now().UTC()) {
		return KnowledgeReadLease{}, model.ErrPolicyDenied
	}
	return l, nil
}
func (s *Store) RevokeKnowledgeReadLease(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE knowledge_read_leases SET revoked=1 WHERE lease_id=?`, id)
	return err
}

func (s *Store) RevokeRuntimeKnowledgeReadLeases(ctx context.Context, runtimeID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE knowledge_read_leases SET revoked=1 WHERE runtime_id=? AND revoked=0`, runtimeID)
	return err
}

func (s *Store) RenewKnowledgeReadLease(ctx context.Context, id, runtimeID string, expires time.Time) error {
	if !expires.After(time.Now().UTC()) {
		return model.ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `UPDATE knowledge_read_leases SET expires_at=? WHERE lease_id=? AND runtime_id=? AND revoked=0 AND expires_at>?`, expires.UTC().Format(time.RFC3339Nano), id, runtimeID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return model.ErrPolicyDenied
	}
	return nil
}
