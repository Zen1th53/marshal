//go:build !governed

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// OwnerAcceptedProjectContract is a project contract the owner accepted in
// the browser that is not active yet: the recorded browser instruction and
// the draft whose exact content it covers.
type OwnerAcceptedProjectContract struct {
	Instruction ProjectContractInstruction
	Draft       MCPProposal
	Submission  ProjectContractSubmission
	Digest      string
}

type projectContractQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// HasUnconsumedBrowserAcceptance reports whether any browser Accept of the
// project is recorded and not yet consumed: the cheap check the open-time
// reconcile makes before it reads anything else.
func (s *Store) HasUnconsumedBrowserAcceptance(ctx context.Context, storage string) (bool, error) {
	if s == nil || s.db == nil || storage == "" {
		return false, nil
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM extreme_project_contract_instructions i WHERE i.project_id=? AND substr(i.instruction_ref,1,8)='browser:'
	AND NOT EXISTS(SELECT 1 FROM extreme_project_contract_instruction_receipts r WHERE r.project_id=i.project_id AND r.instruction_ref=i.instruction_ref)`, storage).Scan(&n)
	if noSuchTable(err) {
		return false, nil
	}
	return n > 0, err
}

// ReadOwnerAcceptedProjectContract finds the newest unconsumed browser Accept
// ("browser:" instruction) made against the active contract version that
// covers the exact canonical contract (id, revision, digest, reviewed
// inventory, expected version) of the newest submitted draft, whatever
// launch submitted it. found is false when nothing waits. It is read only.
func (s *Store) ReadOwnerAcceptedProjectContract(ctx context.Context, project projectcontext.ProjectBinding) (OwnerAcceptedProjectContract, bool, error) {
	if s == nil || s.db == nil || project.StorageID == "" || project.CanonicalID == "" {
		return OwnerAcceptedProjectContract{}, false, nil
	}
	return readOwnerAcceptedProjectContract(ctx, s.db, project)
}

func readOwnerAcceptedProjectContract(ctx context.Context, q projectContractQuery, project projectcontext.ProjectBinding) (OwnerAcceptedProjectContract, bool, error) {
	var current int64
	err := q.QueryRowContext(ctx, `SELECT cas_version FROM extreme_project_contract_active WHERE project_id=?`, project.CanonicalID).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		if noSuchTable(err) {
			return OwnerAcceptedProjectContract{}, false, nil
		}
		return OwnerAcceptedProjectContract{}, false, err
	}
	rows, err := q.QueryContext(ctx, `SELECT i.instruction_ref FROM extreme_project_contract_instructions i
	WHERE i.project_id=? AND i.expected_version=? AND substr(i.instruction_ref,1,8)='browser:'
	AND NOT EXISTS(SELECT 1 FROM extreme_project_contract_instruction_receipts r WHERE r.project_id=i.project_id AND r.instruction_ref=i.instruction_ref)
	ORDER BY i.rowid DESC LIMIT 16`, project.StorageID, current)
	if err != nil {
		if noSuchTable(err) {
			return OwnerAcceptedProjectContract{}, false, nil
		}
		return OwnerAcceptedProjectContract{}, false, err
	}
	var refs []string
	for rows.Next() {
		var ref string
		if err = rows.Scan(&ref); err != nil {
			rows.Close()
			return OwnerAcceptedProjectContract{}, false, err
		}
		refs = append(refs, ref)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return OwnerAcceptedProjectContract{}, false, err
	}
	for _, ref := range refs {
		i, err := readProjectContractInstruction(ctx, q, project.StorageID, ref)
		if err != nil {
			return OwnerAcceptedProjectContract{}, false, err
		}
		if i.digest() != i.BindingDigest || !ArchitectureAcceptanceInstruction(i.Instruction) {
			continue
		}
		found, ok, err := ownerAcceptedDraft(ctx, q, project, i)
		if err != nil || ok {
			return found, ok, err
		}
	}
	return OwnerAcceptedProjectContract{}, false, nil
}

// ownerAcceptedDraft is the newest submitted project-contract draft, when
// instruction i covers its exact content. Only the newest draft counts: a
// Marshal that submitted something different after the owner's Accept is
// not overruled by an older acceptance, and a newest draft the Marshal
// denied, or one already committed or applied, never counts.
func ownerAcceptedDraft(ctx context.Context, q projectContractQuery, project projectcontext.ProjectBinding, i ProjectContractInstruction) (OwnerAcceptedProjectContract, bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT occurrence_id,payload FROM extreme_mcp_proposals WHERE project_id=? ORDER BY rowid DESC LIMIT 256`, project.StorageID)
	if err != nil {
		return OwnerAcceptedProjectContract{}, false, err
	}
	newest := ""
	for rows.Next() {
		var id, payload string
		if err = rows.Scan(&id, &payload); err != nil {
			rows.Close()
			return OwnerAcceptedProjectContract{}, false, err
		}
		var probe struct {
			Action string `json:"action"`
		}
		if json.Unmarshal([]byte(payload), &probe) == nil && probe.Action == "project-contract" {
			newest = id
			break
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || newest == "" {
		return OwnerAcceptedProjectContract{}, false, err
	}
	p, err := readMCPProposal(ctx, q, project.StorageID, newest)
	if err != nil {
		return OwnerAcceptedProjectContract{}, false, err
	}
	switch p.State {
	case "committed", "applied", "denied":
		return OwnerAcceptedProjectContract{}, false, nil
	}
	var sub ProjectContractSubmission
	if json.Unmarshal([]byte(p.Payload), &sub) != nil || sub.Action != "project-contract" || p.Digest != mcpPayloadDigest(p.Payload) {
		return OwnerAcceptedProjectContract{}, false, nil
	}
	c, err := sub.Contract.Canonical()
	if err != nil || c.Project != project {
		return OwnerAcceptedProjectContract{}, false, nil
	}
	sub.Contract = c
	if exact, _ := json.Marshal(sub); string(exact) != p.Payload {
		return OwnerAcceptedProjectContract{}, false, nil
	}
	digest, err := c.CanonicalDigest()
	if err != nil {
		return OwnerAcceptedProjectContract{}, false, nil
	}
	if c.ID != i.ContractID || c.Revision != i.Revision || digest != i.ContractDigest || c.ReviewedInventoryDigest != i.InventoryDigest || sub.ExpectedVersion != i.ExpectedVersion {
		return OwnerAcceptedProjectContract{}, false, nil
	}
	return OwnerAcceptedProjectContract{Instruction: i, Draft: p, Submission: sub, Digest: digest}, true, nil
}

// ApplyOwnerAcceptedProjectContract makes active the contract the owner
// already accepted in the browser when that acceptance was never applied:
// the Marshal's allow Decision was lost to a chat restart, or was refused
// because the identical contract came back under a new launch. The owner's
// browser Accept of that exact revision and digest is the authority; every
// check an acceptance makes still runs (exact reviewed inventory, required
// references, active version). The instruction is consumed exactly once and
// the application is recorded as an event. audienceFor gives the project
// contract owner audience of the draft's owner. applied is false when
// nothing waits.
func (s *Store) ApplyOwnerAcceptedProjectContract(ctx context.Context, project projectcontext.ProjectBinding, audienceFor func(context.Context, string) (ProjectContextAudience, error), now time.Time, reason string) (OwnerAcceptedProjectContract, bool, error) {
	if s == nil || s.db == nil || audienceFor == nil || now.IsZero() {
		return OwnerAcceptedProjectContract{}, false, projectcontext.ErrInvalid
	}
	seen, found, err := s.ReadOwnerAcceptedProjectContract(ctx, project)
	if err != nil || !found {
		return OwnerAcceptedProjectContract{}, false, err
	}
	// The audience reads the store; it is resolved before the write
	// transaction and the transaction then re-reads the exact same choice.
	audience, err := audienceFor(ctx, seen.Draft.Owner)
	if err != nil {
		return OwnerAcceptedProjectContract{}, false, err
	}
	if audience.Worker || audience.Actor != seen.Draft.Owner {
		return OwnerAcceptedProjectContract{}, false, projectcontext.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OwnerAcceptedProjectContract{}, false, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE extreme_mcp_proposals SET state=state WHERE project_id=? AND occurrence_id=?`, project.StorageID, seen.Draft.ID); err != nil {
		return OwnerAcceptedProjectContract{}, false, err
	}
	got, found, err := readOwnerAcceptedProjectContract(ctx, tx, project)
	if err != nil || !found {
		return OwnerAcceptedProjectContract{}, false, err
	}
	if got.Instruction.Ref != seen.Instruction.Ref || got.Draft.ID != seen.Draft.ID || got.Draft.Owner != seen.Draft.Owner || got.Digest != seen.Digest {
		return OwnerAcceptedProjectContract{}, false, model.ErrConflict
	}
	if err = checkProjectContractActivationTx(ctx, tx, got.Submission, audience, now); err != nil {
		return OwnerAcceptedProjectContract{}, false, err
	}
	decision := "owner-accept:" + got.Draft.ID
	if _, err = tx.ExecContext(ctx, `INSERT INTO extreme_project_contract_instruction_receipts VALUES(?,?,?,?,?,?)`, project.StorageID, got.Instruction.Ref, decision, got.Submission.Contract.ID, got.Submission.Contract.Revision, got.Digest); err != nil {
		return OwnerAcceptedProjectContract{}, false, err
	}
	if err = activateProjectContractTx(ctx, tx, got.Submission, got.Digest, got.Draft.ID, now); err != nil {
		return OwnerAcceptedProjectContract{}, false, err
	}
	const settled = "owner browser acceptance applied"
	if got.Draft.State == "admitted" || got.Draft.State == "answer_accepted" {
		if _, err = tx.ExecContext(ctx, `UPDATE extreme_mcp_proposals SET state='committed',reason=? WHERE project_id=? AND occurrence_id=?`, settled, project.StorageID, got.Draft.ID); err != nil {
			return OwnerAcceptedProjectContract{}, false, err
		}
		if err = s.mcpAudit(ctx, tx, project.StorageID, got.Draft.ID, got.Draft.Launch, "committed", settled); err != nil {
			return OwnerAcceptedProjectContract{}, false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE marshal_proposals SET outcome='committed' WHERE project_id=? AND occurrence_id=? AND outcome IN ('','failed')`, project.StorageID, got.Draft.ID); err != nil {
		return OwnerAcceptedProjectContract{}, false, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "owner browser acceptance was recorded but never applied"
	}
	if err = s.AppendEvent(ctx, tx, model.Event{ID: "project-contract-owner-accept-applied-" + got.Instruction.BindingDigest[:32], Type: "PROJECT_CONTRACT_OWNER_ACCEPTANCE_APPLIED", ProjectID: project.StorageID, Timestamp: now.UTC(), Data: map[string]any{"instruction_ref": got.Instruction.Ref, "instruction_launch_id": got.Instruction.Launch, "instruction_occurrence": got.Instruction.ProposalOccurrence, "draft_occurrence": got.Draft.ID, "draft_launch_id": got.Draft.Launch, "draft_state": got.Draft.State, "contract_id": got.Submission.Contract.ID, "revision": got.Submission.Contract.Revision, "contract_digest": got.Digest, "expected_version": got.Submission.ExpectedVersion, "reason": reason}}); err != nil {
		return OwnerAcceptedProjectContract{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return OwnerAcceptedProjectContract{}, false, err
	}
	return got, true, nil
}
