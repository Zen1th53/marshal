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

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func (s *Store) Integrity(ctx context.Context) error {
	var result string
	if err := s.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("SQLite integrity check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("SQLite integrity check: %s", result)
	}
	return nil
}

func (s *Store) Project(ctx context.Context) (model.Project, error) {
	var project model.Project
	err := s.db.QueryRowContext(ctx, `
		SELECT project_id, repository, default_branch, pack_version
		FROM projects ORDER BY project_id LIMIT 1
	`).Scan(&project.ID, &project.Repository, &project.DefaultBranch, &project.PackVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Project{}, fmt.Errorf("%w: project", model.ErrNotFound)
	}
	if err != nil {
		return model.Project{}, fmt.Errorf("read project: %w", err)
	}
	return project, nil
}

func (s *Store) Count(ctx context.Context, table string) (int, error) {
	allowed := map[string]bool{
		"agents": true, "sessions": true, "tasks": true, "leases": true,
		"findings": true, "approvals": true, "artifacts": true, "audit_events": true,
	}
	if !allowed[table] {
		return 0, fmt.Errorf("%w: unsupported count table", model.ErrInvalid)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		return 0, fmt.Errorf("count %s: %w", table, err)
	}
	return count, nil
}

func (s *Store) ListAgents(ctx context.Context) ([]model.Agent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT agent_id, project_id, display_name, role, COALESCE(model_provider, ''),
		       COALESCE(model_name, ''), capabilities_json, status, revision, created_at
		FROM agents ORDER BY agent_id
	`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()
	var agents []model.Agent
	for rows.Next() {
		var agent model.Agent
		var role, status, capabilities, createdAt string
		if err := rows.Scan(&agent.ID, &agent.ProjectID, &agent.DisplayName, &role,
			&agent.ModelProvider, &agent.ModelName, &capabilities, &status,
			&agent.Revision, &createdAt); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		agent.Role = model.Role(role)
		agent.Status = model.AgentStatus(status)
		if err := json.Unmarshal([]byte(capabilities), &agent.Capabilities); err != nil {
			return nil, fmt.Errorf("decode agent capabilities: %w", err)
		}
		agent.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse agent creation: %w", err)
		}
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

func (s *Store) ListTasks(ctx context.Context) ([]model.Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id, title, status, risk, owner_agent_id, branch, worktree,
		       base_commit, head_commit, revision
		FROM tasks ORDER BY task_id
	`)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	var tasks []model.Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate tasks: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close task rows: %w", err)
	}
	for i := range tasks {
		tasks[i].Dependencies, err = s.taskDependencies(ctx, tasks[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return tasks, nil
}

func (s *Store) GetTask(ctx context.Context, taskID string) (model.Task, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT task_id, title, status, risk, owner_agent_id, branch, worktree,
		       base_commit, head_commit, revision
		FROM tasks WHERE task_id = ?
	`, taskID)
	task, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Task{}, fmt.Errorf("%w: task %s", model.ErrNotFound, taskID)
	}
	if err != nil {
		return model.Task{}, fmt.Errorf("read task: %w", err)
	}
	task.Dependencies, err = s.taskDependencies(ctx, task.ID)
	if err != nil {
		return model.Task{}, err
	}
	return task, nil
}

func (s *Store) ActiveLease(ctx context.Context, taskID string) (model.ActiveLease, error) {
	var active model.ActiveLease
	var acquiredAt, expiresAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT l.lease_id, l.task_id, l.session_id, l.acquired_at, l.expires_at,
		       l.revision, l.status, s.agent_id, t.revision
		FROM leases l
		JOIN sessions s ON s.session_id = l.session_id
		JOIN tasks t ON t.task_id = l.task_id
		WHERE l.task_id = ? AND l.status = 'active'
	`, taskID).Scan(&active.Lease.ID, &active.Lease.TaskID, &active.Lease.SessionID,
		&acquiredAt, &expiresAt, &active.Lease.Revision, &active.Lease.Status,
		&active.AgentID, &active.TaskRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ActiveLease{}, fmt.Errorf("%w: active lease for task %s", model.ErrNotFound, taskID)
	}
	if err != nil {
		return model.ActiveLease{}, fmt.Errorf("read active lease: %w", err)
	}
	active.Lease.AcquiredAt, err = time.Parse(time.RFC3339Nano, acquiredAt)
	if err != nil {
		return model.ActiveLease{}, fmt.Errorf("parse lease acquisition: %w", err)
	}
	active.Lease.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return model.ActiveLease{}, fmt.Errorf("parse lease expiry: %w", err)
	}
	return active, nil
}

func (s *Store) ListEvents(ctx context.Context) ([]model.Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, event_type, timestamp, COALESCE(project_id, ''),
		       COALESCE(task_id, ''), COALESCE(actor_agent_id, ''),
		       COALESCE(session_id, ''), aggregate_revision, data_json
		FROM audit_events ORDER BY timestamp, event_id
	`)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	var events []model.Event
	for rows.Next() {
		var event model.Event
		var timestamp, data string
		if err := rows.Scan(&event.ID, &event.Type, &timestamp, &event.ProjectID, &event.TaskID,
			&event.ActorAgentID, &event.SessionID, &event.AggregateRevision, &data); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if event.Timestamp, err = time.Parse(time.RFC3339Nano, timestamp); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &event.Data); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) ListArtifacts(ctx context.Context) ([]model.Artifact, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT artifact_id FROM artifacts ORDER BY created_at, artifact_id")
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var artifacts []model.Artifact
	for _, id := range ids {
		artifact, err := s.GetArtifact(ctx, id)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func (s *Store) ListReferencedArtifactDigests(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT digest FROM artifacts WHERE digest IS NOT NULL AND digest != ''
	`)
	if err != nil {
		return nil, fmt.Errorf("list artifact digests: %w", err)
	}
	defer rows.Close()

	var digests []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		digests = append(digests, d)
	}
	return digests, rows.Err()
}
