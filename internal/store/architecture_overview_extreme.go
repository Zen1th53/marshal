//go:build !governed

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// architectureContract reads the project's accepted project contract: the
// per-project active pointer by the storage project, the accepted and the
// newest revision through the (project_id, contract_id, revision) key,
// and the owner's "accept architecture" instruction that the acceptance
// consumed through the (project_id, decision_occurrence) key.
func (s *Store) architectureContract(ctx context.Context, projectID string, out *ArchitectureOverview) error {
	var canonical, contractID, digest string
	var revision int64
	// The active table holds one pointer row per project.
	err := s.db.QueryRowContext(ctx, `SELECT project_id, contract_id, revision, digest FROM extreme_project_contract_active
		WHERE storage_project_id = ? AND bootstrap_state = 'accepted' LIMIT 1`, projectID).Scan(&canonical, &contractID, &revision, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var body, occurrence, created string
	if err := s.db.QueryRowContext(ctx, `SELECT contract_json, acceptance_occurrence, created_at FROM extreme_project_contract_revisions
		WHERE project_id = ? AND contract_id = ? AND revision = ?`, canonical, contractID, revision).Scan(&body, &occurrence, &created); err != nil {
		return err
	}
	a := &ArchitectureAccepted{Record: ArchitectureRecordContract, ID: contractID, Revision: revision, Digest: digest, By: "Marshal"}
	a.At, _ = time.Parse(time.RFC3339Nano, created)
	var exists int
	err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'extreme_project_contract_instruction_receipts'`).Scan(&exists)
	if err != nil {
		return err
	}
	if exists > 0 {
		var text, at string
		err := s.db.QueryRowContext(ctx, `SELECT i.instruction, i.message_at FROM extreme_project_contract_instruction_receipts r
			JOIN extreme_project_contract_instructions i ON i.project_id = r.project_id AND i.instruction_ref = r.instruction_ref
			WHERE r.project_id = ? AND r.decision_occurrence = ?`, projectID, occurrence).Scan(&text, &at)
		switch {
		case err == nil:
			a.By, a.Instruction = "owner", text
			if t, perr := time.Parse(time.RFC3339Nano, at); perr == nil {
				a.At = t
			}
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
	}
	out.Accepted, out.Comparable, out.Current = a, true, revision
	var newest int64
	var newestBody string
	if err := s.db.QueryRowContext(ctx, `SELECT revision, contract_json FROM extreme_project_contract_revisions
		WHERE project_id = ? AND contract_id = ? ORDER BY revision DESC LIMIT 1`, canonical, contractID).Scan(&newest, &newestBody); err != nil {
		return err
	}
	if newest > revision {
		out.Current = newest
		out.Changes, err = architectureChanges(body, newestBody)
	}
	return err
}
