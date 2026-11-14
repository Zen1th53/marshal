//go:build !governed

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// PendingProjectContract is a project-contract draft the Marshal submitted
// that waits for the owner's "accept architecture": the Board shows it as
// an owner gate. It is read only; accepting stays the owner's chat message,
// bound by RecordProjectContractInstruction.
type PendingProjectContract struct {
	Occurrence string
	Launch     string
	ID         string
	Revision   int64
	Digest     string
	// Purpose is the contract's purpose, its one-line summary.
	Purpose string
	// Submitted is when the draft was admitted.
	Submitted time.Time
	// Contract is the canonical draft itself, as the Marshal submitted it.
	Contract projectcontext.Contract
}

// ReadPendingProjectContract finds the draft an owner "accept architecture"
// would bind to now, by the same rule RecordProjectContractInstruction
// binds: the newest project-contract submission (of launch, when launch is
// set) that is not yet accepted, whose expected version is the active
// contract's version. A draft the owner already instructed for is not
// pending: the owner's part is done and the Marshal's allow is next. found
// is false when nothing waits; a store without the Extreme proposal tables
// has nothing waiting.
func (s *Store) ReadPendingProjectContract(ctx context.Context, project projectcontext.ProjectBinding, launch string) (PendingProjectContract, bool, error) {
	if s == nil || s.db == nil || project.StorageID == "" {
		return PendingProjectContract{}, false, nil
	}
	q := `SELECT p.occurrence_id,p.launch_id,p.digest,p.payload,p.state,a.at FROM extreme_mcp_proposals p
	JOIN extreme_mcp_proposal_audit a ON a.sequence=(SELECT min(sequence) FROM extreme_mcp_proposal_audit WHERE project_id=p.project_id AND occurrence_id=p.occurrence_id AND state='admitted')
	WHERE p.project_id=?`
	args := []any{project.StorageID}
	if launch != "" {
		q += ` AND p.launch_id=?`
		args = append(args, launch)
	}
	q += ` ORDER BY a.sequence DESC LIMIT 64`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		if noSuchTable(err) {
			return PendingProjectContract{}, false, nil
		}
		return PendingProjectContract{}, false, err
	}
	type draft struct{ id, launch, digest, payload, state, at string }
	var found *draft
	for rows.Next() {
		var d draft
		if err = rows.Scan(&d.id, &d.launch, &d.digest, &d.payload, &d.state, &d.at); err != nil {
			rows.Close()
			return PendingProjectContract{}, false, err
		}
		var probe struct {
			Action string `json:"action"`
		}
		if json.Unmarshal([]byte(d.payload), &probe) == nil && probe.Action == "project-contract" {
			found = &d
			break
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || found == nil {
		return PendingProjectContract{}, false, err
	}
	switch found.state {
	case "admitted", "answer_accepted", "applying":
	default:
		// committed, applied, expired, uncertain: nothing for the owner.
		return PendingProjectContract{}, false, nil
	}
	var sub ProjectContractSubmission
	if json.Unmarshal([]byte(found.payload), &sub) != nil || found.digest != mcpPayloadDigest(found.payload) {
		return PendingProjectContract{}, false, nil
	}
	c, err := sub.Contract.Canonical()
	if err != nil || c.Project != project {
		return PendingProjectContract{}, false, nil
	}
	digest, err := c.CanonicalDigest()
	if err != nil {
		return PendingProjectContract{}, false, nil
	}
	var current int64
	var currentID, currentDigest string
	err = s.db.QueryRowContext(ctx, `SELECT cas_version,coalesce(contract_id,''),coalesce(digest,'') FROM extreme_project_contract_active WHERE project_id=?`, project.CanonicalID).Scan(&current, &currentID, &currentDigest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !noSuchTable(err) {
		return PendingProjectContract{}, false, err
	}
	if current != sub.ExpectedVersion {
		// Accepted since, or drafted against another version: an owner
		// instruction would not bind to it.
		return PendingProjectContract{}, false, nil
	}
	if current > 0 && (c.ID != currentID || c.SupersedesDigest != currentDigest || strings.TrimSpace(c.RevisionReason) == "") {
		// A revision that does not supersede the accepted contract can never
		// be accepted, so it does not wait for the owner.
		return PendingProjectContract{}, false, nil
	}
	var instructed int
	err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM extreme_project_contract_instructions WHERE project_id=? AND launch_id=? AND contract_id=? AND revision=? AND contract_digest=? AND expected_version=?`, project.StorageID, found.launch, c.ID, c.Revision, digest, sub.ExpectedVersion).Scan(&instructed)
	if err != nil && !noSuchTable(err) {
		return PendingProjectContract{}, false, err
	}
	if instructed > 0 {
		return PendingProjectContract{}, false, nil
	}
	at, _ := time.Parse(time.RFC3339Nano, found.at)
	return PendingProjectContract{Occurrence: found.id, Launch: found.launch, ID: c.ID, Revision: c.Revision, Digest: digest, Purpose: strings.TrimSpace(c.Purpose), Submitted: at, Contract: c}, true, nil
}

func noSuchTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}
