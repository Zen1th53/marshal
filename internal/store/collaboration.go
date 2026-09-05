package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// SaveTeamSession persists or updates a TeamSession record.
func (s *Store) SaveTeamSession(ctx context.Context, sess model.TeamSession) error {
	if err := sess.Validate(); err != nil {
		return fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}

	participantsJSON, err := json.Marshal(sess.Participants)
	if err != nil {
		return fmt.Errorf("%w: marshal participants: %v", model.ErrInvalid, err)
	}

	for attempt := 0; ; attempt++ {
		err = s.saveTeamSessionTx(ctx, sess, string(participantsJSON))
		if !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return err
		}
		s.observeContention()
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return err
		}
	}
}

func (s *Store) saveTeamSessionTx(ctx context.Context, sess model.TeamSession, participantsJSON string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save team session: %w", err)
	}
	defer tx.Rollback()

	createdAt := sess.CreatedAt.UTC().Format(time.RFC3339Nano)
	updatedAt := sess.UpdatedAt.UTC().Format(time.RFC3339Nano)
	if sess.UpdatedAt.IsZero() {
		updatedAt = createdAt
	}

	query := `
		INSERT INTO team_sessions (
			session_id, goal_id, goal_revision, active_turn,
			turn_sequence, status, participants_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			goal_id = excluded.goal_id,
			goal_revision = excluded.goal_revision,
			active_turn = excluded.active_turn,
			turn_sequence = excluded.turn_sequence,
			status = excluded.status,
			participants_json = excluded.participants_json,
			updated_at = excluded.updated_at
	`
	if _, err := tx.ExecContext(ctx, query,
		sess.SessionID,
		sess.GoalID,
		sess.GoalRevision,
		sess.ActiveTurn,
		sess.TurnSequence,
		sess.Status,
		participantsJSON,
		createdAt,
		updatedAt,
	); err != nil {
		return fmt.Errorf("insert team session: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save team session: %w", err)
	}
	return nil
}

// GetTeamSession retrieves a TeamSession by ID.
func (s *Store) GetTeamSession(ctx context.Context, sessionID string) (*model.TeamSession, error) {
	query := `
		SELECT
			session_id, goal_id, goal_revision, active_turn,
			turn_sequence, status, participants_json, created_at, updated_at
		FROM team_sessions
		WHERE session_id = ?
	`
	row := s.db.QueryRowContext(ctx, query, sessionID)

	var (
		sess             model.TeamSession
		participantsJSON string
		createdAt        string
		updatedAt        string
	)

	err := row.Scan(
		&sess.SessionID,
		&sess.GoalID,
		&sess.GoalRevision,
		&sess.ActiveTurn,
		&sess.TurnSequence,
		&sess.Status,
		&participantsJSON,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: team session not found", model.ErrNotFound)
		}
		return nil, fmt.Errorf("query team session: %w", err)
	}

	if participantsJSON != "" && participantsJSON != "[]" {
		if err := json.Unmarshal([]byte(participantsJSON), &sess.Participants); err != nil {
			return nil, fmt.Errorf("unmarshal participants: %w", err)
		}
	}

	parsedCreated, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		parsedCreated, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
	}
	sess.CreatedAt = parsedCreated

	parsedUpdated, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		parsedUpdated, err = time.Parse(time.RFC3339, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse updated_at: %w", err)
		}
	}
	sess.UpdatedAt = parsedUpdated

	return &sess, nil
}

// SaveAgentMessage records a typed agent-to-agent communication message.
func (s *Store) SaveAgentMessage(ctx context.Context, msg model.AgentMessage) error {
	if err := msg.Validate(); err != nil {
		return fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}

	claimIDsJSON, err := json.Marshal(msg.ClaimIDs)
	if err != nil {
		return fmt.Errorf("%w: marshal claim IDs: %v", model.ErrInvalid, err)
	}
	evidenceIDsJSON, err := json.Marshal(msg.EvidenceIDs)
	if err != nil {
		return fmt.Errorf("%w: marshal evidence IDs: %v", model.ErrInvalid, err)
	}

	for attempt := 0; ; attempt++ {
		err = s.saveAgentMessageTx(ctx, msg, string(claimIDsJSON), string(evidenceIDsJSON))
		if !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return err
		}
		s.observeContention()
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return err
		}
	}
}

func (s *Store) saveAgentMessageTx(ctx context.Context, msg model.AgentMessage, claimIDsJSON, evidenceIDsJSON string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save agent message: %w", err)
	}
	defer tx.Rollback()

	createdAt := msg.CreatedAt.UTC().Format(time.RFC3339Nano)

	query := `
		INSERT INTO agent_messages (
			message_id, session_id, task_id, from_agent, from_harness,
			from_model, to_agent, kind, content, claim_ids_json,
			evidence_ids_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id) DO UPDATE SET
			content = excluded.content,
			claim_ids_json = excluded.claim_ids_json,
			evidence_ids_json = excluded.evidence_ids_json
	`
	if _, err := tx.ExecContext(ctx, query,
		msg.ID,
		msg.SessionID,
		msg.TaskID,
		msg.From.AgentID,
		msg.From.Harness,
		msg.From.Model,
		msg.To,
		string(msg.Kind),
		msg.Content,
		claimIDsJSON,
		evidenceIDsJSON,
		createdAt,
	); err != nil {
		return fmt.Errorf("insert agent message: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save agent message: %w", err)
	}
	return nil
}

// ListAgentMessages retrieves messages for a session ordered by creation time.
func (s *Store) ListAgentMessages(ctx context.Context, sessionID string, limit int) ([]model.AgentMessage, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT
			message_id, session_id, task_id, from_agent, from_harness,
			from_model, to_agent, kind, content, claim_ids_json,
			evidence_ids_json, created_at
		FROM agent_messages
		WHERE session_id = ?
		ORDER BY created_at ASC
		LIMIT ?
	`
	rows, err := s.db.QueryContext(ctx, query, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("query agent messages: %w", err)
	}
	defer rows.Close()

	var result []model.AgentMessage
	for rows.Next() {
		var (
			msg             model.AgentMessage
			fromAgent       string
			fromHarness     string
			fromModel       string
			kindStr         string
			claimIDsJSON    string
			evidenceIDsJSON string
			createdAt       string
		)

		if err := rows.Scan(
			&msg.ID,
			&msg.SessionID,
			&msg.TaskID,
			&fromAgent,
			&fromHarness,
			&fromModel,
			&msg.To,
			&kindStr,
			&msg.Content,
			&claimIDsJSON,
			&evidenceIDsJSON,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent message: %w", err)
		}

		msg.From = model.AuthorProvenance{
			AgentID:   fromAgent,
			Harness:   fromHarness,
			Model:     fromModel,
			SessionID: msg.SessionID,
		}
		msg.Kind = model.MessageKind(kindStr)

		if claimIDsJSON != "" && claimIDsJSON != "[]" {
			_ = json.Unmarshal([]byte(claimIDsJSON), &msg.ClaimIDs)
		}
		if evidenceIDsJSON != "" && evidenceIDsJSON != "[]" {
			_ = json.Unmarshal([]byte(evidenceIDsJSON), &msg.EvidenceIDs)
		}

		parsedTime, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			parsedTime, err = time.Parse(time.RFC3339, createdAt)
			if err != nil {
				return nil, fmt.Errorf("parse created_at: %w", err)
			}
		}
		msg.CreatedAt = parsedTime
		result = append(result, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent messages: %w", err)
	}
	return result, nil
}
