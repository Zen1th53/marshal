//go:build !governed

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// Owner instructions bound to an exact project-contract draft. They are the
// Extreme counterpart of architecture_marshal_instructions for the project
// contract path: the bound live chat observer records the owner's whole
// "accept architecture" message against the newest contract draft of the live
// MCP launch, and the Marshal's allow Decision consumes it exactly once.
const projectContractInstructionSchema = `
CREATE TABLE IF NOT EXISTS extreme_project_contract_instructions (
 project_id TEXT NOT NULL, instruction_ref TEXT NOT NULL, session_id TEXT NOT NULL,
 launch_id TEXT NOT NULL, instruction TEXT NOT NULL, proposal_occurrence TEXT NOT NULL,
 proposal_digest TEXT NOT NULL, contract_id TEXT NOT NULL, revision INTEGER NOT NULL,
 contract_digest TEXT NOT NULL, inventory_digest TEXT NOT NULL, expected_version INTEGER NOT NULL,
 message_at TEXT NOT NULL, binding_digest TEXT NOT NULL,
 PRIMARY KEY(project_id,instruction_ref)
);
CREATE TABLE IF NOT EXISTS extreme_project_contract_instruction_receipts (
 project_id TEXT NOT NULL, instruction_ref TEXT NOT NULL, decision_occurrence TEXT NOT NULL,
 contract_id TEXT NOT NULL, revision INTEGER NOT NULL, contract_digest TEXT NOT NULL,
 PRIMARY KEY(project_id,instruction_ref), UNIQUE(project_id,decision_occurrence),
 FOREIGN KEY(project_id,instruction_ref) REFERENCES extreme_project_contract_instructions(project_id,instruction_ref)
);
CREATE TRIGGER IF NOT EXISTS extreme_project_contract_instructions_no_update BEFORE UPDATE ON extreme_project_contract_instructions BEGIN SELECT RAISE(ABORT,'immutable owner instruction'); END;
CREATE TRIGGER IF NOT EXISTS extreme_project_contract_instructions_no_delete BEFORE DELETE ON extreme_project_contract_instructions BEGIN SELECT RAISE(ABORT,'immutable owner instruction'); END;
CREATE TRIGGER IF NOT EXISTS extreme_project_contract_instruction_receipts_no_update BEFORE UPDATE ON extreme_project_contract_instruction_receipts BEGIN SELECT RAISE(ABORT,'immutable instruction receipt'); END;
CREATE TRIGGER IF NOT EXISTS extreme_project_contract_instruction_receipts_no_delete BEFORE DELETE ON extreme_project_contract_instruction_receipts BEGIN SELECT RAISE(ABORT,'immutable instruction receipt'); END;
`

// ErrNoPendingProjectContract means there is no unaccepted contract draft of
// the live Marshal launch for an owner instruction to bind to.
var ErrNoPendingProjectContract = errors.New("no pending project contract draft from the live Marshal")

// ErrProjectContractInstructionMissing refuses a Marshal allow Decision that
// no recorded owner instruction covers.
var ErrProjectContractInstructionMissing = fmt.Errorf("%w: project contract acceptance needs the owner's recorded \"accept architecture\" instruction for this exact contract", model.ErrPolicyDenied)

// ProjectContractInstruction is the exact binding of one owner message.
type ProjectContractInstruction struct {
	Ref                string `json:"instruction_ref"`
	Session            string `json:"session_id"`
	Launch             string `json:"launch_id"`
	Instruction        string `json:"instruction"`
	ProposalOccurrence string `json:"proposal_occurrence"`
	ProposalDigest     string `json:"proposal_digest"`
	ContractID         string `json:"contract_id"`
	Revision           int64  `json:"revision"`
	ContractDigest     string `json:"contract_digest"`
	InventoryDigest    string `json:"inventory_digest"`
	ExpectedVersion    int64  `json:"expected_version"`
	MessageAt          string `json:"message_at"`
	BindingDigest      string `json:"-"`
}

func (i ProjectContractInstruction) digest() string {
	raw, _ := json.Marshal(i)
	sum := sha256.Sum256(append([]byte("marshal-project-contract-instruction-v1\x00"), raw...))
	return hex.EncodeToString(sum[:])
}

// RecordProjectContractInstruction is called only by the bound live Marshal
// chat observer with a whole owner message. It binds the message to the newest
// project-contract draft of the live MCP launch that was admitted BEFORE the
// message and is not yet accepted. Repeating the same ref is idempotent.
func (s *Store) RecordProjectContractInstruction(ctx context.Context, project projectcontext.ProjectBinding, session, launch, ref, text string, messageAt time.Time) (ProjectContractInstruction, bool, error) {
	if s == nil || s.db == nil || project.StorageID == "" || session == "" || launch == "" || ref == "" || len(ref) > 200 || messageAt.IsZero() || !ArchitectureAcceptanceInstruction(text) {
		return ProjectContractInstruction{}, false, model.ErrPolicyDenied
	}
	if _, err := s.db.ExecContext(ctx, projectContractInstructionSchema); err != nil {
		return ProjectContractInstruction{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProjectContractInstruction{}, false, err
	}
	defer tx.Rollback()
	if existing, err := readProjectContractInstruction(ctx, tx, project.StorageID, ref); err == nil {
		if existing.Session != session || existing.Launch != launch || existing.Instruction != text {
			return ProjectContractInstruction{}, false, model.ErrPolicyDenied
		}
		return existing, false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return ProjectContractInstruction{}, false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.occurrence_id,p.digest,p.payload,p.state,a.at FROM extreme_mcp_proposals p
	JOIN extreme_mcp_proposal_audit a ON a.sequence=(SELECT min(sequence) FROM extreme_mcp_proposal_audit WHERE project_id=p.project_id AND occurrence_id=p.occurrence_id AND state='admitted')
	WHERE p.project_id=? AND p.launch_id=? ORDER BY a.sequence DESC LIMIT 64`, project.StorageID, launch)
	if err != nil {
		return ProjectContractInstruction{}, false, err
	}
	type draft struct{ id, digest, payload, state, at string }
	var found *draft
	for rows.Next() {
		var d draft
		if err = rows.Scan(&d.id, &d.digest, &d.payload, &d.state, &d.at); err != nil {
			rows.Close()
			return ProjectContractInstruction{}, false, err
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
	if err != nil {
		return ProjectContractInstruction{}, false, err
	}
	if found == nil || found.state == "committed" || found.state == "applied" {
		return ProjectContractInstruction{}, false, ErrNoPendingProjectContract
	}
	admitted, err := time.Parse(time.RFC3339Nano, found.at)
	if err != nil || !messageAt.After(admitted) {
		// An owner message older than the draft never approves that draft.
		return ProjectContractInstruction{}, false, ErrNoPendingProjectContract
	}
	var sub ProjectContractSubmission
	if err = json.Unmarshal([]byte(found.payload), &sub); err != nil || found.digest != mcpPayloadDigest(found.payload) {
		return ProjectContractInstruction{}, false, projectcontext.Changed("project_contract_instruction_extreme.go:138")
	}
	c, err := sub.Contract.Canonical()
	if err != nil || c.Project != project {
		return ProjectContractInstruction{}, false, projectcontext.Changed("project_contract_instruction_extreme.go:142")
	}
	contractDigest, err := c.CanonicalDigest()
	if err != nil {
		return ProjectContractInstruction{}, false, err
	}
	var current int64
	err = tx.QueryRowContext(ctx, `SELECT cas_version FROM extreme_project_contract_active WHERE project_id=?`, project.CanonicalID).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !strings.Contains(err.Error(), "no such table") {
		return ProjectContractInstruction{}, false, err
	}
	if current != sub.ExpectedVersion {
		return ProjectContractInstruction{}, false, ErrNoPendingProjectContract
	}
	i := ProjectContractInstruction{Ref: ref, Session: session, Launch: launch, Instruction: text, ProposalOccurrence: found.id, ProposalDigest: found.digest, ContractID: c.ID, Revision: c.Revision, ContractDigest: contractDigest, InventoryDigest: c.ReviewedInventoryDigest, ExpectedVersion: sub.ExpectedVersion, MessageAt: messageAt.UTC().Format(time.RFC3339Nano)}
	i.BindingDigest = i.digest()
	if _, err = tx.ExecContext(ctx, `INSERT INTO extreme_project_contract_instructions VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, project.StorageID, i.Ref, i.Session, i.Launch, i.Instruction, i.ProposalOccurrence, i.ProposalDigest, i.ContractID, i.Revision, i.ContractDigest, i.InventoryDigest, i.ExpectedVersion, i.MessageAt, i.BindingDigest); err != nil {
		return ProjectContractInstruction{}, false, err
	}
	if err = s.AppendEvent(ctx, tx, model.Event{ID: "project-contract-instruction-" + i.BindingDigest[:32], Type: "PROJECT_CONTRACT_OWNER_INSTRUCTION", ProjectID: project.StorageID, Timestamp: time.Now().UTC(), Data: map[string]any{"instruction_ref": i.Ref, "session_id": i.Session, "launch_id": i.Launch, "proposal_occurrence": i.ProposalOccurrence, "contract_id": i.ContractID, "revision": i.Revision, "contract_digest": i.ContractDigest, "binding_digest": i.BindingDigest}}); err != nil {
		return ProjectContractInstruction{}, false, err
	}
	return i, true, tx.Commit()
}

func readProjectContractInstruction(ctx context.Context, q mcpQuery, project, ref string) (ProjectContractInstruction, error) {
	var i ProjectContractInstruction
	err := q.QueryRowContext(ctx, `SELECT instruction_ref,session_id,launch_id,instruction,proposal_occurrence,proposal_digest,contract_id,revision,contract_digest,inventory_digest,expected_version,message_at,binding_digest FROM extreme_project_contract_instructions WHERE project_id=? AND instruction_ref=?`, project, ref).Scan(&i.Ref, &i.Session, &i.Launch, &i.Instruction, &i.ProposalOccurrence, &i.ProposalDigest, &i.ContractID, &i.Revision, &i.ContractDigest, &i.InventoryDigest, &i.ExpectedVersion, &i.MessageAt, &i.BindingDigest)
	return i, err
}

// consumeProjectContractInstructionTx binds the newest unconsumed owner
// instruction for this exact contract, launch and expected version to the
// applying decision inside the acceptance transaction.
func consumeProjectContractInstructionTx(ctx context.Context, tx *sql.Tx, project, launch, decision string, sub ProjectContractSubmission, contractDigest string) (string, error) {
	if _, err := tx.ExecContext(ctx, projectContractInstructionSchema); err != nil {
		return "", err
	}
	var ref string
	err := tx.QueryRowContext(ctx, `SELECT i.instruction_ref FROM extreme_project_contract_instructions i
	WHERE i.project_id=? AND i.launch_id=? AND i.contract_id=? AND i.revision=? AND i.contract_digest=? AND i.inventory_digest=? AND i.expected_version=?
	AND NOT EXISTS(SELECT 1 FROM extreme_project_contract_instruction_receipts r WHERE r.project_id=i.project_id AND r.instruction_ref=i.instruction_ref)
	ORDER BY i.rowid DESC LIMIT 1`, project, launch, sub.Contract.ID, sub.Contract.Revision, contractDigest, sub.Contract.ReviewedInventoryDigest, sub.ExpectedVersion).Scan(&ref)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrProjectContractInstructionMissing
	}
	if err != nil {
		return "", err
	}
	i, err := readProjectContractInstruction(ctx, tx, project, ref)
	if err != nil {
		return "", err
	}
	if i.digest() != i.BindingDigest || !ArchitectureAcceptanceInstruction(i.Instruction) {
		return "", ErrProjectContractInstructionMissing
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO extreme_project_contract_instruction_receipts VALUES(?,?,?,?,?,?)`, project, ref, decision, sub.Contract.ID, sub.Contract.Revision, contractDigest); err != nil {
		return "", err
	}
	return ref, nil
}

// ProjectContractInstructionReceipt returns the owner instruction that the
// accepted contract revision consumed.
func (s *Store) ProjectContractInstructionReceipt(ctx context.Context, project, contractID string, revision int64) (ProjectContractInstruction, string, error) {
	var ref, decision string
	err := s.db.QueryRowContext(ctx, `SELECT instruction_ref,decision_occurrence FROM extreme_project_contract_instruction_receipts WHERE project_id=? AND contract_id=? AND revision=?`, project, contractID, revision).Scan(&ref, &decision)
	if err != nil {
		return ProjectContractInstruction{}, "", err
	}
	i, err := readProjectContractInstruction(ctx, s.db, project, ref)
	if err != nil {
		return ProjectContractInstruction{}, "", err
	}
	if i.digest() != i.BindingDigest {
		return ProjectContractInstruction{}, "", projectcontext.Changed("project_contract_instruction_extreme.go:217")
	}
	return i, decision, nil
}
