package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/slaves/internal/model"
)

const schemaV1 = `
CREATE TABLE projects (
	project_id TEXT PRIMARY KEY,
	repository TEXT NOT NULL UNIQUE,
	default_branch TEXT NOT NULL,
	pack_version TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE agents (
	agent_id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(project_id),
	display_name TEXT NOT NULL,
	role TEXT NOT NULL CHECK(role IN ('orchestrator','architect','developer','qa','appsec')),
	model_provider TEXT,
	model_name TEXT,
	capabilities_json TEXT NOT NULL DEFAULT '[]',
	status TEXT NOT NULL CHECK(status IN ('registered','active','disabled')),
	revision INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0),
	created_at TEXT NOT NULL
);
CREATE TABLE tasks (
	task_id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(project_id),
	title TEXT NOT NULL,
	status TEXT NOT NULL CHECK(status IN (
		'proposed','ready','claimed','working','blocked','review','qa',
		'security_review','ready_to_merge','merged','cancelled','superseded'
	)),
	risk TEXT NOT NULL CHECK(risk IN ('R0','R1','R2','R3')),
	owner_agent_id TEXT REFERENCES agents(agent_id),
	branch TEXT,
	worktree TEXT,
	base_commit TEXT,
	head_commit TEXT,
	revision INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE sessions (
	session_id TEXT PRIMARY KEY,
	agent_id TEXT NOT NULL REFERENCES agents(agent_id),
	project_id TEXT NOT NULL REFERENCES projects(project_id),
	task_id TEXT REFERENCES tasks(task_id),
	role TEXT NOT NULL CHECK(role IN ('orchestrator','architect','developer','qa','appsec')),
	branch TEXT,
	worktree TEXT,
	started_at TEXT NOT NULL,
	last_heartbeat TEXT NOT NULL,
	status TEXT NOT NULL CHECK(status IN ('active','stale','failed','terminated')),
	revision INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0)
);
CREATE TABLE task_dependencies (
	task_id TEXT NOT NULL REFERENCES tasks(task_id) ON DELETE CASCADE,
	depends_on_task_id TEXT NOT NULL REFERENCES tasks(task_id),
	kind TEXT NOT NULL DEFAULT 'hard' CHECK(kind IN ('hard')),
	PRIMARY KEY(task_id, depends_on_task_id),
	CHECK(task_id <> depends_on_task_id)
);
CREATE TABLE leases (
	lease_id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL REFERENCES tasks(task_id),
	session_id TEXT NOT NULL REFERENCES sessions(session_id),
	acquired_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	revision INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0),
	status TEXT NOT NULL CHECK(status IN ('active','released','expired','revoked'))
);
CREATE UNIQUE INDEX one_active_lease_per_task ON leases(task_id) WHERE status = 'active';
CREATE TABLE decisions (
	decision_id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(project_id),
	owner_role TEXT NOT NULL,
	status TEXT NOT NULL,
	body TEXT NOT NULL,
	source TEXT NOT NULL,
	revision INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE findings (
	finding_id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(project_id),
	owner_role TEXT NOT NULL CHECK(owner_role IN ('qa','appsec')),
	severity TEXT NOT NULL CHECK(severity IN ('BLOCKER','HIGH','MEDIUM','LOW')),
	status TEXT NOT NULL CHECK(status IN ('open','assigned','fixing','ready_for_retest','closed','accepted_risk')),
	task_id TEXT REFERENCES tasks(task_id),
	title TEXT NOT NULL,
	evidence_json TEXT NOT NULL DEFAULT '[]',
	revision INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE handoffs (
	handoff_id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL REFERENCES tasks(task_id),
	from_role TEXT NOT NULL,
	to_role TEXT NOT NULL,
	from_session_id TEXT REFERENCES sessions(session_id),
	body TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE checkpoints (
	checkpoint_id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL REFERENCES tasks(task_id),
	session_id TEXT NOT NULL REFERENCES sessions(session_id),
	repository_commit TEXT NOT NULL,
	state_json TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE approvals (
	approval_id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(project_id),
	operation TEXT NOT NULL,
	scope TEXT NOT NULL,
	target TEXT,
	requested_by TEXT NOT NULL,
	approved_by TEXT,
	status TEXT NOT NULL CHECK(status IN ('requested','approved','denied','expired','consumed','revoked')),
	commit_hash TEXT,
	conditions_json TEXT NOT NULL DEFAULT '[]',
	created_at TEXT NOT NULL,
	expires_at TEXT,
	revision INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0)
);
CREATE TABLE artifacts (
	artifact_id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(project_id),
	kind TEXT NOT NULL,
	digest TEXT NOT NULL UNIQUE,
	source_commit TEXT NOT NULL,
	producer_session_id TEXT REFERENCES sessions(session_id),
	task_ids_json TEXT NOT NULL DEFAULT '[]',
	verification_refs_json TEXT NOT NULL DEFAULT '[]',
	path TEXT NOT NULL,
	size_bytes INTEGER NOT NULL CHECK(size_bytes >= 0),
	created_at TEXT NOT NULL
);
CREATE TABLE audit_events (
	event_id TEXT PRIMARY KEY,
	event_type TEXT NOT NULL,
	project_id TEXT REFERENCES projects(project_id),
	task_id TEXT REFERENCES tasks(task_id),
	actor_agent_id TEXT REFERENCES agents(agent_id),
	session_id TEXT REFERENCES sessions(session_id),
	aggregate_revision INTEGER NOT NULL CHECK(aggregate_revision >= 0),
	timestamp TEXT NOT NULL,
	data_json TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE memory_records (
	memory_id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(project_id),
	memory_type TEXT NOT NULL,
	status TEXT NOT NULL,
	confidence TEXT NOT NULL,
	body TEXT NOT NULL,
	provenance_json TEXT NOT NULL,
	last_verified_at TEXT,
	revision INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE worker_runs (
	run_id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL REFERENCES tasks(task_id),
	session_id TEXT NOT NULL REFERENCES sessions(session_id),
	adapter TEXT NOT NULL,
	adapter_version TEXT NOT NULL,
	base_commit TEXT NOT NULL,
	result_commit TEXT,
	started_at TEXT NOT NULL,
	ended_at TEXT,
	exit_status INTEGER,
	status TEXT NOT NULL,
	stdout_artifact_id TEXT REFERENCES artifacts(artifact_id),
	stderr_artifact_id TEXT REFERENCES artifacts(artifact_id),
	verification_json TEXT NOT NULL DEFAULT '[]',
	revision INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0)
);
CREATE TABLE verifications (
	verification_id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL REFERENCES tasks(task_id),
	session_id TEXT REFERENCES sessions(session_id),
	commit_hash TEXT NOT NULL,
	command_json TEXT NOT NULL,
	exit_status INTEGER NOT NULL,
	output_digest TEXT NOT NULL,
	valid INTEGER NOT NULL CHECK(valid IN (0,1)),
	created_at TEXT NOT NULL,
	invalidated_at TEXT
);
`

func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	var version int
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > 2 {
		return fmt.Errorf("database schema version %d is newer than supported version 2", version)
	}
	if version == 0 {
		if _, err := tx.ExecContext(ctx, schemaV1); err != nil {
			return fmt.Errorf("apply schema version 1: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(1, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 1: %w", err)
		}
		version = 1
	}
	if version == 1 {
		v2Statements := []string{
			"ALTER TABLE worker_runs ADD COLUMN runtime_instance_id TEXT;",
			"ALTER TABLE worker_runs ADD COLUMN process_start_identity TEXT;",
			"ALTER TABLE worker_runs ADD COLUMN cancellation_requested_at TEXT;",
			"ALTER TABLE worker_runs ADD COLUMN recovery_epoch INTEGER DEFAULT 0;",
		}
		for _, stmt := range v2Statements {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("apply schema version 2 statement (%s): %w", stmt, err)
			}
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(2, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 2: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

func (s *Store) InitProject(ctx context.Context, project model.Project) error {
	if project.ID == "" || project.Repository == "" || project.DefaultBranch == "" || project.PackVersion == "" {
		return fmt.Errorf("%w: incomplete project identity", model.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin project initialization: %w", err)
	}
	defer tx.Rollback()

	var repository, branch, pack string
	err = tx.QueryRowContext(ctx, `
		SELECT repository, default_branch, pack_version
		FROM projects WHERE project_id = ?
	`, project.ID).Scan(&repository, &branch, &pack)
	switch {
	case err == nil:
		if repository != project.Repository || branch != project.DefaultBranch || pack != project.PackVersion {
			return fmt.Errorf("%w: project %s has different identity", model.ErrConflict, project.ID)
		}
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO projects(project_id, repository, default_branch, pack_version, created_at)
			VALUES(?, ?, ?, ?, ?)
		`, project.ID, project.Repository, project.DefaultBranch, project.PackVersion, utcNow()); err != nil {
			return fmt.Errorf("insert project: %w", err)
		}
	default:
		return fmt.Errorf("read project: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit project initialization: %w", err)
	}
	return nil
}

func utcNow() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
