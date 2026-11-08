package store

import (
	"context"
	"fmt"
	"sort"
)

type OperatorReadGrant struct {
	Folder  string `json:"folder"`
	Allowed bool   `json:"allowed"`
}

func (s *Store) SetOperatorReadGrant(ctx context.Context, projectIdentity, folder string, allowed bool) error {
	if projectIdentity == "" || folder == "" {
		return fmt.Errorf("invalid operator read grant")
	}
	decision := 0
	if allowed {
		decision = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO operator_read_grants(project_identity,folder,allowed,updated_at)
		VALUES(?,?,?,?) ON CONFLICT(project_identity,folder) DO UPDATE SET allowed=excluded.allowed,updated_at=excluded.updated_at`, projectIdentity, folder, decision, utcNow())
	if err != nil {
		return fmt.Errorf("persist operator read grant: %w", err)
	}
	return nil
}

func (s *Store) ListOperatorReadGrants(ctx context.Context, projectIdentity string) ([]OperatorReadGrant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT folder,allowed FROM operator_read_grants WHERE project_identity=? ORDER BY folder`, projectIdentity)
	if err != nil {
		return nil, fmt.Errorf("list operator read grants: %w", err)
	}
	defer rows.Close()
	var grants []OperatorReadGrant
	for rows.Next() {
		var grant OperatorReadGrant
		var allowed int
		if err := rows.Scan(&grant.Folder, &allowed); err != nil {
			return nil, fmt.Errorf("scan operator read grant: %w", err)
		}
		grant.Allowed = allowed == 1
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read operator read grants: %w", err)
	}
	sort.Slice(grants, func(i, j int) bool { return grants[i].Folder < grants[j].Folder })
	return grants, nil
}

func (s *Store) RevokeOperatorReadGrant(ctx context.Context, projectIdentity, folder string) error {
	if projectIdentity == "" || folder == "" {
		return fmt.Errorf("invalid operator read grant")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM operator_read_grants WHERE project_identity=? AND folder=?`, projectIdentity, folder); err != nil {
		return fmt.Errorf("revoke operator read grant: %w", err)
	}
	return nil
}
