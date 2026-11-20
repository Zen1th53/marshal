//go:build !governed

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Zen1th53/marshal/internal/model"
	"time"
)

// MCPProposal retains source identity separately from display labels and IDs.
type MCPProposal struct{ ID, Launch, Owner, Provider, Submission, Digest, Payload, State, Reason string }

// InitMCPProposals is private additive storage. It deliberately does not change
// the shared schema version: default runtimes ignore these private tables.
func (s *Store) InitMCPProposals(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS extreme_mcp_proposals(project_id TEXT NOT NULL,occurrence_id TEXT NOT NULL,launch_id TEXT NOT NULL,owner_id TEXT NOT NULL,provider TEXT NOT NULL,submission_id TEXT NOT NULL,digest TEXT NOT NULL,payload TEXT NOT NULL,state TEXT NOT NULL,reason TEXT NOT NULL DEFAULT '',PRIMARY KEY(project_id,occurrence_id),UNIQUE(project_id,launch_id,submission_id))`,
		`CREATE TABLE IF NOT EXISTS extreme_mcp_proposal_audit(sequence INTEGER PRIMARY KEY AUTOINCREMENT,project_id TEXT NOT NULL,occurrence_id TEXT NOT NULL,launch_id TEXT NOT NULL,state TEXT NOT NULL,reason TEXT NOT NULL,at TEXT NOT NULL)`} {
		if _, err = tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) AdmitMCPProposal(ctx context.Context, project string, p MCPProposal) (MCPProposal, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return p, false, err
	}
	defer tx.Rollback()
	saved, fresh, err := s.admitMCPProposalTx(ctx, tx, project, p)
	if err != nil {
		return saved, false, err
	}
	return saved, fresh, tx.Commit()
}
func (s *Store) admitMCPProposalTx(ctx context.Context, tx *sql.Tx, project string, p MCPProposal) (MCPProposal, bool, error) {
	sum := sha256.Sum256([]byte(p.Payload))
	p.Digest = hex.EncodeToString(sum[:])
	p.State = "admitted"
	old, err := readMCPProposal(ctx, tx, project, p.ID)
	if err == nil {
		if old.Digest != p.Digest || old.Launch != p.Launch || old.Submission != p.Submission {
			return old, false, fmt.Errorf("submission key already binds different payload")
		}
		return old, false, nil
	}
	if err != sql.ErrNoRows {
		return p, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO extreme_mcp_proposals(project_id,occurrence_id,launch_id,owner_id,provider,submission_id,digest,payload,state) VALUES(?,?,?,?,?,?,?,?,?)`, project, p.ID, p.Launch, p.Owner, p.Provider, p.Submission, p.Digest, p.Payload, p.State)
	if err != nil {
		return p, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO marshal_proposals(project_id,occurrence_id,line) VALUES(?,?,?)`, project, p.ID, "MARSHAL_PROPOSAL "+p.Payload)
	if err != nil {
		return p, false, err
	}
	if err = s.mcpAudit(ctx, tx, project, p.ID, p.Launch, p.State, ""); err != nil {
		return p, false, err
	}
	return p, true, nil
}

type mcpQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readMCPProposal(ctx context.Context, q mcpQuery, project, id string) (MCPProposal, error) {
	var p MCPProposal
	err := q.QueryRowContext(ctx, `SELECT occurrence_id,launch_id,owner_id,provider,submission_id,digest,payload,state,reason FROM extreme_mcp_proposals WHERE project_id=? AND occurrence_id=?`, project, id).Scan(&p.ID, &p.Launch, &p.Owner, &p.Provider, &p.Submission, &p.Digest, &p.Payload, &p.State, &p.Reason)
	return p, err
}
func (s *Store) MCPProposal(ctx context.Context, project, id string) (MCPProposal, error) {
	return readMCPProposal(ctx, s.db, project, id)
}
func (s *Store) mcpAudit(ctx context.Context, tx *sql.Tx, project, id, launch, state, reason string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO extreme_mcp_proposal_audit(project_id,occurrence_id,launch_id,state,reason,at) VALUES(?,?,?,?,?,?)`, project, id, launch, state, reason, utcNow())
	if err != nil {
		return err
	}
	p, err := readMCPProposal(ctx, tx, project, id)
	if err != nil {
		return err
	}
	return s.AppendEvent(ctx, tx, model.Event{ID: id + "-" + state, Type: "MARSHAL_MCP_PROPOSAL", ProjectID: project, Timestamp: time.Now().UTC(), Data: map[string]any{"occurrence_id": id, "launch_id": launch, "owner_id": p.Owner, "provider": p.Provider, "submission_id": p.Submission, "payload_digest": p.Digest, "state": state, "reason": reason}})
}
func (s *Store) SetMCPProposalState(ctx context.Context, project, id, state, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p, err := readMCPProposal(ctx, tx, project, id)
	if err != nil {
		return err
	}
	if p.State == state {
		return nil
	}
	if p.State != "admitted" && p.State != "answer_accepted" && p.State != "applying" && p.State != "committed" {
		return fmt.Errorf("proposal receipt is terminal")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE extreme_mcp_proposals SET state=?,reason=? WHERE project_id=? AND occurrence_id=?`, state, reason, project, id); err != nil {
		return err
	}
	if err = s.mcpAudit(ctx, tx, project, id, p.Launch, state, reason); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) ExpireMCPLaunch(ctx context.Context, project, launch string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT occurrence_id FROM extreme_mcp_proposals WHERE project_id=? AND launch_id=? AND state IN ('admitted','answer_accepted','applying')`, project, launch)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		p, err := s.MCPProposal(ctx, project, id)
		if err != nil {
			return err
		}
		state := "expired"
		if p.State == "applying" {
			state = "uncertain"
		}
		if err = s.SetMCPProposalState(ctx, project, id, state, "launch revoked"); err != nil {
			return err
		}
		if err = s.ResolveMarshalProposal(ctx, project, id, state); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) MCPProposalLegacyOutcome(ctx context.Context, project, id string) (string, error) {
	var outcome string
	err := s.db.QueryRowContext(ctx, `SELECT outcome FROM marshal_proposals WHERE project_id=? AND occurrence_id=?`, project, id).Scan(&outcome)
	return outcome, err
}

// AcceptMCPDecision binds the exact pending authority request and its accepted
// answer in one transaction. The raw answer remains in the source payload;
// acceptance does not prove the permission effect was applied.
func (s *Store) AcceptMCPDecision(ctx context.Context, project, id, request, key string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p, err := readMCPProposal(ctx, tx, project, id)
	if err != nil {
		return err
	}
	if p.State != "admitted" {
		return fmt.Errorf("decision is not newly admitted")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE marshal_proposals SET request_json=?,request_key=? WHERE project_id=? AND occurrence_id=? AND outcome='' AND request_json=''`, request, key, project, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE extreme_mcp_proposals SET state='answer_accepted',reason='pending runtime application' WHERE project_id=? AND occurrence_id=?`, project, id); err != nil {
		return err
	}
	if err = s.mcpAudit(ctx, tx, project, id, p.Launch, "answer_accepted", "pending runtime application"); err != nil {
		return err
	}
	return tx.Commit()
}

// RebindMCPLaunch carries the open submissions of a Marshal chat launch that
// the Board restarted (same role, same provider, a new session) to the
// launch that replaced it: admitted rows of from move to to and its owner,
// with their journalled requests, and the move is audited. Memory
// submissions stay with their launch (their candidates name it), as do
// answers (decide) and rows already applying: a changed launch authorizes
// nothing it did not submit itself. The caller holds the live lease authority for
// to and must have minted it for the restarted chat itself; nothing a model
// says can reach this. It returns the moved occurrence IDs.
func (s *Store) RebindMCPLaunch(ctx context.Context, project, from, to, owner, provider string) ([]string, error) {
	if from == "" || to == "" || from == to || owner == "" || provider == "" {
		return nil, fmt.Errorf("rebind needs two distinct launches")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT occurrence_id,payload FROM extreme_mcp_proposals WHERE project_id=? AND launch_id=? AND provider=? AND state='admitted'`, project, from, provider)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id, payload string
		if err = rows.Scan(&id, &payload); err != nil {
			rows.Close()
			return nil, err
		}
		var probe struct {
			Action string `json:"action"`
		}
		if json.Unmarshal([]byte(payload), &probe) == nil && (probe.Action == "memory" || probe.Action == "decide") {
			continue
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	oldJSON, newJSON := `"SourceLaunch":"`+from+`"`, `"SourceLaunch":"`+to+`"`
	oldKey, newKey := "\x00"+from+"\x00", "\x00"+to+"\x00"
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, `UPDATE extreme_mcp_proposals SET launch_id=?,owner_id=? WHERE project_id=? AND occurrence_id=? AND launch_id=?`, to, owner, project, id, from); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE marshal_proposals SET request_json=replace(request_json,?,?),request_key=replace(request_key,?,?) WHERE project_id=? AND occurrence_id=?`, oldJSON, newJSON, oldKey, newKey, project, id); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO extreme_mcp_proposal_audit(project_id,occurrence_id,launch_id,state,reason,at) VALUES(?,?,?,?,?,?)`, project, id, to, "rebound", "restarted Marshal chat; from launch "+from, utcNow()); err != nil {
			return nil, err
		}
	}
	if len(ids) > 0 {
		if err = s.AppendEvent(ctx, tx, model.Event{ID: "mcp-rebind-" + to, Type: "MARSHAL_MCP_LAUNCH_REBOUND", ProjectID: project, Timestamp: time.Now().UTC(), Data: map[string]any{"from_launch_id": from, "to_launch_id": to, "owner_id": owner, "provider": provider, "occurrences": ids}}); err != nil {
			return nil, err
		}
	}
	return ids, tx.Commit()
}

// MCPProposalBySubmission finds a launch's submission by its submission ID.
// A submission carried from a restarted chat keeps the occurrence ID it was
// admitted under, so the live launch's receipts look it up this way.
func (s *Store) MCPProposalBySubmission(ctx context.Context, project, launch, submission string) (MCPProposal, error) {
	var p MCPProposal
	err := s.db.QueryRowContext(ctx, `SELECT occurrence_id,launch_id,owner_id,provider,submission_id,digest,payload,state,reason FROM extreme_mcp_proposals WHERE project_id=? AND launch_id=? AND submission_id=?`, project, launch, submission).Scan(&p.ID, &p.Launch, &p.Owner, &p.Provider, &p.Submission, &p.Digest, &p.Payload, &p.State, &p.Reason)
	return p, err
}
