package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// CommitCollaborationCommand writes an operator message and, for a handoff,
// the session's advanced turn, together with the command result and audit
// record, in one transaction. The session must still be at the envelope's
// expected turn sequence; a stale handoff or message is a conflict. The result
// version is the session's turn sequence after the command.
func (s *Store) CommitCollaborationCommand(ctx context.Context, r CommandRecord, msg model.AgentMessage, activeTurn string) (int64, error) {
	if err := msg.Validate(); err != nil {
		return 0, fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}
	claims, err := json.Marshal(msg.ClaimIDs)
	if err != nil {
		return 0, err
	}
	evidence, err := json.Marshal(msg.EvidenceIDs)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var sequence int64
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT turn_sequence, status FROM team_sessions WHERE session_id=?`, msg.SessionID).Scan(&sequence, &status); err != nil {
		return 0, fmt.Errorf("%w: collaboration session %s", model.ErrNotFound, msg.SessionID)
	}
	if sequence != r.ExpectedVersion {
		return 0, fmt.Errorf("%w: session turn moved from %d to %d", model.ErrConflict, r.ExpectedVersion, sequence)
	}
	if status != "ACTIVE" {
		return 0, fmt.Errorf("%w: collaboration session is %s", model.ErrConflict, status)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_messages (
			message_id, session_id, task_id, from_agent, from_harness,
			from_model, to_agent, kind, content, claim_ids_json,
			evidence_ids_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.SessionID, msg.TaskID, msg.From.AgentID, msg.From.Harness,
		msg.From.Model, msg.To, msg.Kind, msg.Content, string(claims),
		string(evidence), msg.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return 0, fmt.Errorf("insert operator message: %w", err)
	}
	result := sequence
	if activeTurn != "" {
		result = sequence + 1
		if _, err := tx.ExecContext(ctx, `UPDATE team_sessions SET active_turn=?, turn_sequence=?, updated_at=? WHERE session_id=? AND turn_sequence=?`,
			activeTurn, result, utcNow(), msg.SessionID, sequence); err != nil {
			return 0, fmt.Errorf("advance session turn: %w", err)
		}
	}
	if err := insertCommandReceipt(ctx, tx, r, result); err != nil {
		return 0, err
	}
	return result, tx.Commit()
}
