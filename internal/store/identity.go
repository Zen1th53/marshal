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

func (s *Store) RegisterAgent(ctx context.Context, agent model.Agent) error {
	if agent.ID == "" || agent.ProjectID == "" || agent.DisplayName == "" || !agent.Role.Valid() {
		return fmt.Errorf("%w: incomplete agent identity", model.ErrInvalid)
	}
	if agent.Status == "" {
		agent.Status = model.AgentRegistered
	}
	if agent.Status != model.AgentRegistered && agent.Status != model.AgentActive && agent.Status != model.AgentDisabled {
		return fmt.Errorf("%w: invalid agent status %q", model.ErrInvalid, agent.Status)
	}
	if agent.Capabilities == nil {
		agent.Capabilities = []string{}
	}
	capabilities, err := json.Marshal(agent.Capabilities)
	if err != nil {
		return fmt.Errorf("%w: encode agent capabilities: %v", model.ErrInvalid, err)
	}
	createdAt := agent.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin agent registration: %w", err)
	}
	defer tx.Rollback()

	var displayName, role, provider, modelName, storedCapabilities, status string
	var revision int64
	err = tx.QueryRowContext(ctx, `
		SELECT display_name, role, COALESCE(model_provider, ''), COALESCE(model_name, ''),
		       capabilities_json, status, revision
		FROM agents WHERE agent_id = ?
	`, agent.ID).Scan(&displayName, &role, &provider, &modelName, &storedCapabilities, &status, &revision)
	switch {
	case err == nil:
		if displayName != agent.DisplayName || role != string(agent.Role) ||
			provider != agent.ModelProvider || modelName != agent.ModelName ||
			storedCapabilities != string(capabilities) || status != string(agent.Status) ||
			revision != agent.Revision {
			return fmt.Errorf("%w: agent %s has different identity", model.ErrConflict, agent.ID)
		}
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO agents(
				agent_id, project_id, display_name, role, model_provider, model_name,
				capabilities_json, status, revision, created_at
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, agent.ID, agent.ProjectID, agent.DisplayName, agent.Role, nullIfEmpty(agent.ModelProvider),
			nullIfEmpty(agent.ModelName), string(capabilities), agent.Status, agent.Revision,
			createdAt.Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("insert agent: %w", err)
		}
	default:
		return fmt.Errorf("read agent: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit agent registration: %w", err)
	}
	return nil
}

func (s *Store) GetAgent(ctx context.Context, agentID string) (model.Agent, error) {
	if agentID == "" {
		return model.Agent{}, fmt.Errorf("%w: agent ID is required", model.ErrInvalid)
	}
	var agent model.Agent
	var role, status, capabilities, createdAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT agent_id, project_id, display_name, role, COALESCE(model_provider, ''),
		       COALESCE(model_name, ''), capabilities_json, status, revision, created_at
		FROM agents WHERE agent_id = ?
	`, agentID).Scan(&agent.ID, &agent.ProjectID, &agent.DisplayName, &role,
		&agent.ModelProvider, &agent.ModelName, &capabilities, &status,
		&agent.Revision, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Agent{}, fmt.Errorf("%w: agent %s", model.ErrNotFound, agentID)
	}
	if err != nil {
		return model.Agent{}, fmt.Errorf("read agent: %w", err)
	}
	agent.Role = model.Role(role)
	agent.Status = model.AgentStatus(status)
	if err := json.Unmarshal([]byte(capabilities), &agent.Capabilities); err != nil {
		return model.Agent{}, fmt.Errorf("decode agent capabilities: %w", err)
	}
	agent.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return model.Agent{}, fmt.Errorf("parse agent creation: %w", err)
	}
	return agent, nil
}

func (s *Store) UpdateAgent(ctx context.Context, agent model.Agent, expectedRevision int64) (model.Agent, error) {
	if agent.ID == "" {
		return model.Agent{}, fmt.Errorf("%w: agent ID is required", model.ErrInvalid)
	}
	if agent.DisplayName == "" {
		return model.Agent{}, fmt.Errorf("%w: display name is required", model.ErrInvalid)
	}
	if agent.Status != "" && agent.Status != model.AgentRegistered && agent.Status != model.AgentActive && agent.Status != model.AgentDisabled {
		return model.Agent{}, fmt.Errorf("%w: invalid agent status %q", model.ErrInvalid, agent.Status)
	}
	if agent.Capabilities == nil {
		agent.Capabilities = []string{}
	}
	capabilities, err := json.Marshal(agent.Capabilities)
	if err != nil {
		return model.Agent{}, fmt.Errorf("%w: encode agent capabilities: %v", model.ErrInvalid, err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Agent{}, fmt.Errorf("begin update agent: %w", err)
	}
	defer tx.Rollback()

	var existingRole string
	var currentRevision int64
	err = tx.QueryRowContext(ctx, `
		SELECT role, revision FROM agents WHERE agent_id = ?
	`, agent.ID).Scan(&existingRole, &currentRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Agent{}, fmt.Errorf("%w: agent %s", model.ErrNotFound, agent.ID)
	}
	if err != nil {
		return model.Agent{}, fmt.Errorf("read agent for update: %w", err)
	}
	if expectedRevision >= 0 && currentRevision != expectedRevision {
		return model.Agent{}, fmt.Errorf("%w: agent revision conflict (current=%d, expected=%d)", model.ErrConflict, currentRevision, expectedRevision)
	}
	if agent.Role != "" && agent.Role != model.Role(existingRole) {
		return model.Agent{}, fmt.Errorf("%w: agent role is immutable", model.ErrConflict)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE agents
		SET display_name = ?, model_provider = ?, model_name = ?,
		    capabilities_json = ?, status = ?, revision = revision + 1
		WHERE agent_id = ? AND revision = ?
	`, agent.DisplayName, nullIfEmpty(agent.ModelProvider), nullIfEmpty(agent.ModelName),
		string(capabilities), agent.Status, agent.ID, currentRevision)
	if err != nil {
		return model.Agent{}, fmt.Errorf("update agent: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return model.Agent{}, fmt.Errorf("check update agent rows: %w", err)
	}
	if rows != 1 {
		return model.Agent{}, fmt.Errorf("%w: agent update conflict", model.ErrConflict)
	}

	if err := tx.Commit(); err != nil {
		return model.Agent{}, fmt.Errorf("commit update agent: %w", err)
	}

	return s.GetAgent(ctx, agent.ID)
}

func (s *Store) DeleteAgent(ctx context.Context, agentID string, expectedRevision int64) error {
	if agentID == "" {
		return fmt.Errorf("%w: agent ID is required", model.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete agent: %w", err)
	}
	defer tx.Rollback()

	var currentRevision int64
	var activeSessions int
	err = tx.QueryRowContext(ctx, `SELECT revision FROM agents WHERE agent_id = ?`, agentID).Scan(&currentRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: agent %s", model.ErrNotFound, agentID)
	}
	if err != nil {
		return fmt.Errorf("read agent for deletion: %w", err)
	}
	if expectedRevision >= 0 && currentRevision != expectedRevision {
		return fmt.Errorf("%w: agent revision conflict (current=%d, expected=%d)", model.ErrConflict, currentRevision, expectedRevision)
	}

	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE agent_id = ? AND status = 'active'`, agentID).Scan(&activeSessions); err != nil {
		return fmt.Errorf("check active sessions: %w", err)
	}
	if activeSessions > 0 {
		return fmt.Errorf("%w: cannot delete agent with %d active sessions", model.ErrConflict, activeSessions)
	}

	var activeTasks int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE owner_agent_id = ? AND status IN ('claimed', 'working')`, agentID).Scan(&activeTasks); err != nil {
		return fmt.Errorf("check active tasks: %w", err)
	}
	if activeTasks > 0 {
		return fmt.Errorf("%w: cannot delete agent assigned to %d active tasks", model.ErrConflict, activeTasks)
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM agents WHERE agent_id = ? AND revision = ?`, agentID, currentRevision)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check delete rows: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: agent delete conflict", model.ErrConflict)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete agent: %w", err)
	}
	return nil
}

func (s *Store) StartSession(ctx context.Context, start model.SessionStart) (model.Session, error) {
	if start.ID == "" || start.AgentID == "" || start.ProjectID == "" {
		return model.Session{}, fmt.Errorf("%w: incomplete session identity", model.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Session{}, fmt.Errorf("begin session: %w", err)
	}
	defer tx.Rollback()

	var role string
	var status string
	var projectID string
	if err := tx.QueryRowContext(ctx, `
		SELECT role, status, project_id FROM agents WHERE agent_id = ?
	`, start.AgentID).Scan(&role, &status, &projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Session{}, fmt.Errorf("%w: agent %s", model.ErrNotFound, start.AgentID)
		}
		return model.Session{}, fmt.Errorf("read session agent: %w", err)
	}
	if projectID != start.ProjectID || status == string(model.AgentDisabled) {
		return model.Session{}, fmt.Errorf("%w: agent cannot start this session", model.ErrConflict)
	}
	now := time.Now().UTC()
	session := model.Session{
		ID: start.ID, AgentID: start.AgentID, ProjectID: start.ProjectID,
		Role: model.Role(role), Branch: start.Branch, Worktree: start.Worktree,
		StartedAt: now, LastHeartbeat: now, Status: model.SessionActive,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sessions(
			session_id, agent_id, project_id, role, branch, worktree,
			started_at, last_heartbeat, status, revision
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
	`, session.ID, session.AgentID, session.ProjectID, session.Role,
		nullIfEmpty(session.Branch), nullIfEmpty(session.Worktree),
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), session.Status); err != nil {
		return model.Session{}, fmt.Errorf("insert session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.Session{}, fmt.Errorf("commit session: %w", err)
	}
	return session, nil
}

func (s *Store) Heartbeat(ctx context.Context, sessionID string, at time.Time, expectedRevision int64) error {
	if at.IsZero() {
		return fmt.Errorf("%w: heartbeat time is required", model.ErrInvalid)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions
		SET last_heartbeat = ?, revision = revision + 1
		WHERE session_id = ? AND status = 'active' AND revision = ?
	`, at.UTC().Format(time.RFC3339Nano), sessionID, expectedRevision)
	if err != nil {
		return fmt.Errorf("update heartbeat: %w", err)
	}
	return requireOne(result, "heartbeat session")
}

func (s *Store) GetSession(ctx context.Context, sessionID string) (model.Session, error) {
	var session model.Session
	var role, status, started, heartbeat string
	var taskID sql.NullString
	var branch, worktree sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT session_id, agent_id, project_id, task_id, role, branch, worktree,
		       started_at, last_heartbeat, status, revision
		FROM sessions WHERE session_id = ?
	`, sessionID).Scan(&session.ID, &session.AgentID, &session.ProjectID, &taskID, &role,
		&branch, &worktree, &started, &heartbeat, &status, &session.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Session{}, fmt.Errorf("%w: session %s", model.ErrNotFound, sessionID)
	}
	if err != nil {
		return model.Session{}, fmt.Errorf("read session: %w", err)
	}
	if taskID.Valid {
		session.TaskID = &taskID.String
	}
	session.Role = model.Role(role)
	session.Status = model.SessionStatus(status)
	session.Branch = branch.String
	session.Worktree = worktree.String
	session.StartedAt, err = time.Parse(time.RFC3339Nano, started)
	if err != nil {
		return model.Session{}, fmt.Errorf("parse session start: %w", err)
	}
	session.LastHeartbeat, err = time.Parse(time.RFC3339Nano, heartbeat)
	if err != nil {
		return model.Session{}, fmt.Errorf("parse session heartbeat: %w", err)
	}
	return session, nil
}

func (s *Store) TerminateSession(ctx context.Context, sessionID string, status model.SessionStatus, expectedRevision int64) error {
	if status != model.SessionStale && status != model.SessionFailed && status != model.SessionTerminated {
		return fmt.Errorf("%w: invalid terminal session status %q", model.ErrInvalid, status)
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET status = ?, revision = revision + 1
		WHERE session_id = ? AND status = 'active' AND revision = ?
	`, status, sessionID, expectedRevision)
	if err != nil {
		return fmt.Errorf("terminate session: %w", err)
	}
	return requireOne(result, "terminate session")
}

func requireOne(result sql.Result, operation string) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", operation, err)
	}
	if count != 1 {
		return fmt.Errorf("%w: %s changed %d rows", model.ErrConflict, operation, count)
	}
	return nil
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}
