//go:build !governed

package store

import (
	"context"
	"encoding/json"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// ProjectContractRevision describes an acceptance, even if no commit or run
// followed it. Commit history alone loses revisions accepted at the same HEAD.
type ProjectContractRevision struct {
	Revision   int64                   `json:"revision"`
	Date       string                  `json:"date"`
	Goal       string                  `json:"goal"`
	Run        string                  `json:"run"`
	AcceptedBy string                  `json:"accepted_by"`
	Contract   projectcontext.Contract `json:"-"`
	Digest     string                  `json:"digest"`
}

func (s *Store) ProjectContractRevisions(ctx context.Context, project projectcontext.ProjectBinding) ([]ProjectContractRevision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.revision,r.created_at,coalesce(p.owner_id,''),r.contract_json,r.digest
FROM extreme_project_contract_revisions r
LEFT JOIN extreme_mcp_proposals p ON p.project_id=? AND p.occurrence_id=r.acceptance_occurrence
WHERE r.project_id=? ORDER BY r.revision DESC LIMIT 200`, project.StorageID, project.CanonicalID)
	if err != nil {
		return nil, err
	}
	list := []ProjectContractRevision{}
	for rows.Next() {
		var entry ProjectContractRevision
		var raw string
		if err = rows.Scan(&entry.Revision, &entry.Date, &entry.AcceptedBy, &raw, &entry.Digest); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &entry.Contract); err != nil {
			rows.Close()
			return nil, err
		}
		digest, digestErr := entry.Contract.CanonicalDigest()
		if digestErr != nil || digest != entry.Digest || entry.Contract.Project != project {
			rows.Close()
			return nil, projectcontext.Changed("project contract history digest")
		}
		list = append(list, entry)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// The immutable acceptance instruction identifies the session. Look up its
	// goal as it was at acceptance time, never today's active goal. Older
	// acceptances without an instruction leave the goal explicitly unknown.
	var instructions int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='extreme_project_contract_instructions'`).Scan(&instructions); err != nil {
		return nil, err
	}
	for i := range list {
		entry := &list[i]
		if instructions > 0 {
			err = s.db.QueryRowContext(ctx, `SELECT coalesce((SELECT g.goal_id FROM goal_contracts g
JOIN extreme_project_contract_instructions i ON i.session_id=g.session_id
JOIN extreme_project_contract_instruction_receipts receipt ON receipt.project_id=i.project_id AND receipt.instruction_ref=i.instruction_ref
WHERE i.project_id=? AND i.contract_id=? AND i.revision=? AND i.contract_digest=?
AND g.project_id=? AND g.created_at<=? AND g.updated_at<=?
ORDER BY g.updated_at DESC,g.revision DESC LIMIT 1),'')`, project.StorageID, entry.Contract.ID, entry.Revision, entry.Digest, project.CanonicalID, entry.Date, entry.Date).Scan(&entry.Goal)
			if err != nil {
				return nil, err
			}
		}
		// A later execution packet records the actual run using this revision.
		// Do not guess a run from whichever one is active when the page opens.
		err = s.db.QueryRowContext(ctx, `SELECT coalesce((SELECT json_extract(binding_json,'$.run_id') FROM extreme_project_context_receipts
WHERE project_id=? AND contract_id=? AND contract_revision=? AND outcome='QUALIFIED'
AND coalesce(json_extract(binding_json,'$.run_id'),'')<>''
AND (?='' OR json_extract(binding_json,'$.goal_id')=?) ORDER BY created_at LIMIT 1),'')`, project.CanonicalID, entry.Contract.ID, entry.Revision, entry.Goal, entry.Goal).Scan(&entry.Run)
		if err != nil {
			return nil, err
		}
	}
	return list, nil
}
