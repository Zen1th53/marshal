//go:build !governed

package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
)

// MCPMemoryCandidate is a safe, immutable pending snapshot, not canonical
// retained memory. Runtime authority is held only by the current launch.
type MCPMemoryCandidate struct {
	Occurrence, ImportKey, PayloadDigest string
	Record                               model.MemoryRecordV2
	State                                string
}

func (s *Store) InitMCPMemory(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "PRAGMA synchronous = FULL"); err != nil {
		return err
	}
	var durability int
	if err := s.db.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&durability); err != nil {
		return err
	}
	if durability != 2 {
		return fmt.Errorf("private memory requires SQLite FULL synchronization")
	}
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS extreme_mcp_memory_candidates(project_id TEXT NOT NULL,occurrence_id TEXT NOT NULL,import_key TEXT NOT NULL,payload_digest TEXT NOT NULL,memory_id TEXT NOT NULL,record_json TEXT NOT NULL,PRIMARY KEY(project_id,occurrence_id),UNIQUE(project_id,memory_id))`)
	return err
}
func readMCPMemory(ctx context.Context, q mcpQuery, project, id string) (MCPMemoryCandidate, error) {
	var c MCPMemoryCandidate
	var raw string
	err := q.QueryRowContext(ctx, `SELECT c.occurrence_id,c.import_key,c.payload_digest,c.record_json,p.state FROM extreme_mcp_memory_candidates c JOIN extreme_mcp_proposals p ON p.project_id=c.project_id AND p.occurrence_id=c.occurrence_id WHERE c.project_id=? AND c.occurrence_id=?`, project, id).Scan(&c.Occurrence, &c.ImportKey, &c.PayloadDigest, &raw, &c.State)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &c.Record)
	}
	return c, err
}
func (s *Store) MCPMemory(ctx context.Context, project, id string) (MCPMemoryCandidate, error) {
	return readMCPMemory(ctx, s.db, project, id)
}

// MCPMemoryByRecordID identifies broker-managed records from the canonical
// source journal, including expired submissions. Caller labels are not proof.
// An ordinary continuation runtime may not have initialized this private table.
func (s *Store) MCPMemoryByRecordID(ctx context.Context, project, memoryID string) (MCPMemoryCandidate, error) {
	var initialized bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='extreme_mcp_memory_candidates')`).Scan(&initialized); err != nil {
		return MCPMemoryCandidate{}, err
	}
	if !initialized {
		return s.legacyMemoryOrigin(ctx, project, memoryID)
	}
	var occurrence string
	if err := s.db.QueryRowContext(ctx, `SELECT occurrence_id FROM extreme_mcp_memory_candidates WHERE project_id=? AND memory_id=?`, project, memoryID).Scan(&occurrence); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s.legacyMemoryOrigin(ctx, project, memoryID)
		}
		return MCPMemoryCandidate{}, err
	}
	return s.MCPMemory(ctx, project, occurrence)
}
func (s *Store) FindMCPMemory(ctx context.Context, project, key, digest string) (MCPMemoryCandidate, error) {
	var id string
	query := `SELECT c.occurrence_id FROM extreme_mcp_memory_candidates c JOIN extreme_mcp_proposals p ON p.project_id=c.project_id AND p.occurrence_id=c.occurrence_id WHERE c.project_id=? AND c.import_key=?`
	args := []any{project, key}
	if digest != "" {
		query += ` AND c.payload_digest=?`
		args = append(args, digest)
	}
	query += ` ORDER BY CASE WHEN p.state IN ('committed','applied') THEN 0 ELSE 1 END,c.rowid DESC LIMIT 1`
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&id)
	if err != nil {
		return MCPMemoryCandidate{}, err
	}
	return s.MCPMemory(ctx, project, id)
}
func (s *Store) AdmitMCPMemory(ctx context.Context, project string, p MCPProposal, c MCPMemoryCandidate) (MCPMemoryCandidate, bool, error) {
	if c.Record.ProjectID != project || c.Record.Scope != "project" || c.Record.ScopeID != project || c.Record.Lifecycle != model.MemoryCandidate || c.Record.Authority != model.AuthorityAgent {
		return c, false, model.ErrInvalid
	}
	// Scan before any journal, and again at canonical write time. No raw refusal body is retained.
	// Extreme: a secret in the candidate is removed before anything is
	// journaled; the record names the redacted kinds, never the value.
	rec, _, err := security.NewFirewall(security.FirewallConfig{}).AdmitRecord(ctx, c.Record)
	if err != nil {
		return c, false, fmt.Errorf("memory candidate rejected: %w", err)
	}
	c.Record = rec
	if err := c.Record.Validate(); err != nil {
		return c, false, err
	}
	raw, err := json.Marshal(c.Record)
	if err != nil {
		return c, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return c, false, err
	}
	defer tx.Rollback()
	old, err := readMCPMemory(ctx, tx, project, p.ID)
	if err == nil {
		if old.PayloadDigest != c.PayloadDigest {
			return c, false, fmt.Errorf("submission key binds different memory payload")
		}
		return old, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return c, false, err
	}
	_, fresh, err := s.admitMCPProposalTx(ctx, tx, project, p)
	if err != nil {
		return c, false, err
	}
	if !fresh {
		return c, false, fmt.Errorf("submission key already binds a proposal")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO extreme_mcp_memory_candidates(project_id,occurrence_id,import_key,payload_digest,memory_id,record_json) VALUES(?,?,?,?,?,?)`, project, p.ID, c.ImportKey, c.PayloadDigest, c.Record.ID, string(raw))
	if err != nil {
		return c, false, err
	}
	c.Occurrence = p.ID
	c.State = "admitted"
	return c, true, tx.Commit()
}

// CommitMCPMemoryDecision is only called after Runtime.CommandPermission has
// authenticated local control and the live broker has claimed application.
// Canonical row + checked outbox + permission evidence + committed receipt
// share one transaction; no successful receipt can precede the canonical row.
func (s *Store) CommitMCPMemoryDecision(ctx context.Context, project, id string, allow bool, rec model.MemoryRecordV2, audit model.Event) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c, err := readMCPMemory(ctx, tx, project, id)
	if err != nil {
		return err
	}
	if c.State == "committed" || c.State == "applied" {
		if allow {
			return nil
		}
		return model.ErrConflict
	}
	if c.State != "admitted" && c.State != "applying" {
		return fmt.Errorf("memory proposal is not pending")
	}
	state := "denied"
	if allow {
		actualJSON, actualErr := json.Marshal(rec)
		trustedJSON, trustedErr := json.Marshal(c.Record)
		if actualErr != nil || trustedErr != nil || !bytes.Equal(actualJSON, trustedJSON) {
			return fmt.Errorf("memory snapshot differs from admitted candidate")
		}
		rec = c.Record
		// The operator's decision retains the memory. Recall serves only active
		// lifecycles, so the retained row must not stay a review candidate.
		rec.Lifecycle = model.MemoryDurable
		if err = s.writeMemoryV2Tx(ctx, tx, rec); err != nil {
			return err
		}
		state = "committed"
	}
	if err = s.AppendEvent(ctx, tx, audit); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE extreme_mcp_proposals SET state=?,reason=? WHERE project_id=? AND occurrence_id=?`, state, "canonical memory decision committed", project, id)
	if err != nil {
		return err
	}
	p, err := readMCPProposal(ctx, tx, project, id)
	if err != nil {
		return err
	}
	if err = s.mcpAudit(ctx, tx, project, id, p.Launch, state, "canonical memory decision committed"); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE marshal_proposals SET outcome=? WHERE project_id=? AND occurrence_id=? AND outcome=''`, state, project, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
