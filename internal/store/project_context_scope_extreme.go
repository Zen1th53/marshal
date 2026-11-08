//go:build !governed

package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// ValidateProjectContractScope checks contract metadata only. This early route
// check never grants invocation or reads memory bodies; the actual callback
// must still qualify the current complete packet, ACL and owned execution.
func (s *Store) ValidateProjectContractScope(ctx context.Context, project projectcontext.ProjectBinding, reviewed projectcontext.ContractRef, selection projectcontext.Selection) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, _, digest, err := readProjectContract(ctx, tx, project)
	if err != nil {
		return err
	}
	if reviewed == (projectcontext.ContractRef{ID: current.ID, Revision: current.Revision, Digest: digest}) {
		return nil
	}
	var raw, previousDigest string
	if err := tx.QueryRowContext(ctx, `SELECT contract_json,digest FROM extreme_project_contract_revisions WHERE project_id=? AND contract_id=? AND revision=? AND digest=?`, project.CanonicalID, reviewed.ID, reviewed.Revision, reviewed.Digest).Scan(&raw, &previousDigest); err != nil {
		return projectcontext.Changed("project_context_scope_extreme.go:31")
	}
	var previous projectcontext.Contract
	if json.Unmarshal([]byte(raw), &previous) != nil || previous.Project != project {
		return projectcontext.Changed("project_context_scope_extreme.go:35")
	}
	canonical, err := previous.CanonicalDigest()
	if err != nil || canonical != previousDigest {
		return projectcontext.Changed("project_context_scope_extreme.go:39")
	}
	before, err := projectcontext.ApplicableContractDigest(previous, selection)
	if err != nil {
		return err
	}
	after, err := projectcontext.ApplicableContractDigest(current, selection)
	if err != nil || before != after {
		return projectcontext.Changed("project_context_scope_extreme.go:47")
	}
	return nil
}
