package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

const LatestSchemaVersion = 60
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
	if version > LatestSchemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", version, LatestSchemaVersion)
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
		version = 2
	}
	if version == 2 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE evidence_nodes (
				node_id TEXT PRIMARY KEY,
				node_type TEXT NOT NULL CHECK(node_type IN ('claim','command','output','artifact','environment','verification','policy-decision')),
				digest TEXT NOT NULL UNIQUE,
				metadata_json TEXT NOT NULL,
				created_at TEXT NOT NULL
			);
			CREATE INDEX evidence_nodes_by_type ON evidence_nodes(node_type);
			CREATE INDEX evidence_nodes_by_digest ON evidence_nodes(digest);
			CREATE TABLE evidence_edges (
				from_node_id TEXT NOT NULL REFERENCES evidence_nodes(node_id),
				to_node_id TEXT NOT NULL REFERENCES evidence_nodes(node_id),
				relation TEXT NOT NULL,
				created_at TEXT NOT NULL,
				PRIMARY KEY(from_node_id, to_node_id, relation),
				CHECK(from_node_id <> to_node_id)
			);
			CREATE INDEX evidence_edges_by_to ON evidence_edges(to_node_id);
		`); err != nil {
			return fmt.Errorf("apply schema version 3: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(3, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 3: %w", err)
		}
		version = 3
	}
	if version == 3 {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE evidence_nodes ADD COLUMN state TEXT NOT NULL DEFAULT 'stored' CHECK(state IN ('draft','stored','linked','archived','exported'))`); err != nil {
			return fmt.Errorf("apply schema version 4: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(4, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 4: %w", err)
		}
		version = 4
	}
	if version == 4 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE policy_versions (
				policy_id TEXT NOT NULL,
				version INTEGER NOT NULL CHECK(version > 0),
				digest TEXT NOT NULL,
				generation INTEGER NOT NULL CHECK(generation >= 0),
				source_ref TEXT NOT NULL DEFAULT '',
				normalized_rules TEXT NOT NULL,
				status TEXT NOT NULL CHECK(status IN ('draft','active','retired')),
				activated_at TEXT,
				PRIMARY KEY(policy_id, version),
				UNIQUE(policy_id, version, digest)
			);
			CREATE INDEX policy_versions_by_digest ON policy_versions(digest);
			CREATE INDEX policy_versions_by_generation ON policy_versions(policy_id, generation);
		`); err != nil {
			return fmt.Errorf("apply schema version 5: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(5, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 5: %w", err)
		}
		version = 5
	}
	if version == 5 {
		if _, err := tx.ExecContext(ctx, `
			ALTER TABLE policy_versions ADD COLUMN state TEXT NOT NULL DEFAULT 'loaded'
			CHECK(state IN ('loaded','validated','active','superseded'));
		`); err != nil {
			return fmt.Errorf("apply schema version 6: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(6, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 6: %w", err)
		}
		version = 6
	}
	if version == 6 {
		var active int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM policy_versions WHERE state = 'active'").Scan(&active); err != nil {
			return fmt.Errorf("check active policy uniqueness: %w", err)
		}
		if active > 1 {
			return fmt.Errorf("active policy set is ambiguous: %d active policies", active)
		}
		if _, err := tx.ExecContext(ctx, `
			CREATE UNIQUE INDEX policy_versions_one_active
			ON policy_versions(state) WHERE state = 'active'
		`); err != nil {
			return fmt.Errorf("apply schema version 7: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(7, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 7: %w", err)
		}
		version = 7
	}
	if version == 7 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE policy_test_runs (
				run_id TEXT PRIMARY KEY,
				policy_id TEXT NOT NULL,
				policy_version INTEGER NOT NULL CHECK(policy_version > 0),
				policy_digest TEXT NOT NULL,
				generation INTEGER NOT NULL CHECK(generation >= 0),
				test_file_digest TEXT NOT NULL,
				created_at TEXT NOT NULL,
				content_digest TEXT NOT NULL
			);
			CREATE INDEX policy_test_runs_by_policy
				ON policy_test_runs(policy_id, policy_version, policy_digest, generation);
			CREATE TABLE policy_test_cases (
				run_id TEXT NOT NULL REFERENCES policy_test_runs(run_id) ON DELETE CASCADE,
				case_id TEXT NOT NULL,
				status TEXT NOT NULL CHECK(status IN ('PASS','FAIL','ERROR','SKIP')),
				diff TEXT NOT NULL DEFAULT '',
				reason TEXT NOT NULL DEFAULT '',
				PRIMARY KEY(run_id, case_id)
			);
			CREATE INDEX policy_test_cases_by_status
				ON policy_test_cases(run_id, status);
		`); err != nil {
			return fmt.Errorf("apply schema version 8: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(8, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 8: %w", err)
		}
		version = 8
	}
	if version == 8 {
		if _, err := tx.ExecContext(ctx, `
			ALTER TABLE policy_test_runs ADD COLUMN state TEXT NOT NULL DEFAULT 'loaded'
			CHECK(state IN ('loaded','validated','executed','passed','failed'));
		`); err != nil {
			return fmt.Errorf("apply schema version 9: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(9, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 9: %w", err)
		}
		version = 9
	}
	if version == 9 {
		if _, err := tx.ExecContext(ctx, `
			ALTER TABLE policy_test_runs ADD COLUMN execution_owner TEXT NOT NULL DEFAULT '';
			ALTER TABLE policy_test_runs ADD COLUMN execution_claimed_at TEXT NOT NULL DEFAULT '';
		`); err != nil {
			return fmt.Errorf("apply schema version 10: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(10, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 10: %w", err)
		}
		version = 10
	}
	if version == 10 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE policy_test_outcomes (
				run_id TEXT NOT NULL REFERENCES policy_test_runs(run_id) ON DELETE CASCADE,
				case_id TEXT NOT NULL,
				status TEXT NOT NULL CHECK(status IN ('PASS','FAIL','ERROR','SKIP')),
				diff TEXT NOT NULL DEFAULT '',
				reason TEXT NOT NULL DEFAULT '',
				PRIMARY KEY(run_id, case_id)
			);
			CREATE INDEX policy_test_outcomes_by_status
				ON policy_test_outcomes(run_id, status);
		`); err != nil {
			return fmt.Errorf("apply schema version 11: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(11, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 11: %w", err)
		}
		version = 11
	}
	if version == 11 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS structured_events (
				event_id TEXT NOT NULL UNIQUE, sequence INTEGER PRIMARY KEY AUTOINCREMENT,
				event_type TEXT NOT NULL, subject TEXT NOT NULL DEFAULT '', task_id TEXT NOT NULL DEFAULT '',
				run_id TEXT NOT NULL DEFAULT '', resource_id TEXT NOT NULL DEFAULT '', evidence_id TEXT NOT NULL DEFAULT '',
				at TEXT NOT NULL, data_json TEXT NOT NULL DEFAULT '{}', idempotency_key TEXT UNIQUE
			);
			CREATE UNIQUE INDEX IF NOT EXISTS structured_events_by_sequence ON structured_events(sequence);
			CREATE INDEX IF NOT EXISTS structured_events_by_task_sequence ON structured_events(task_id, sequence);
			CREATE INDEX IF NOT EXISTS structured_events_by_run_sequence ON structured_events(run_id, sequence);
			CREATE INDEX IF NOT EXISTS structured_events_by_type_sequence ON structured_events(event_type, sequence);
			CREATE TABLE dag_nodes (
				task_id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK(kind IN ('task','gate')),
				status TEXT NOT NULL CHECK(status IN ('pending','ready','running','completed','failed','blocked','skipped')),
				priority INTEGER NOT NULL, revision INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0),
				created_at TEXT NOT NULL, updated_at TEXT NOT NULL
			);
			CREATE INDEX dag_nodes_by_status_priority ON dag_nodes(status, priority DESC, task_id);
			CREATE TABLE dag_edges (
				from_task TEXT NOT NULL REFERENCES dag_nodes(task_id) ON DELETE CASCADE,
				to_task TEXT NOT NULL REFERENCES dag_nodes(task_id) ON DELETE CASCADE,
				condition TEXT NOT NULL CHECK(condition IN ('completed','failed','blocked','skipped')),
				created_at TEXT NOT NULL, PRIMARY KEY(from_task, to_task), CHECK(from_task <> to_task)
			);
			CREATE INDEX dag_edges_by_from ON dag_edges(from_task, to_task);
			CREATE INDEX dag_edges_by_to ON dag_edges(to_task, from_task);
		`); err != nil {
			return fmt.Errorf("apply schema version 12: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(12, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 12: %w", err)
		}
		version = 12
	}
	if version == 12 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE capability_grants (
				id TEXT PRIMARY KEY,
				subject TEXT NOT NULL,
				task_id TEXT NOT NULL,
				kind TEXT NOT NULL CHECK(kind IN ('fs.read','fs.write','shell.exec','git.commit','git.push','network.egress','secret.use','mcp.call','deploy.execute')),
				resource TEXT NOT NULL,
				actions_json TEXT NOT NULL,
				constraints_json TEXT NOT NULL,
				issuer TEXT NOT NULL,
				issued_at TEXT NOT NULL,
				expires_at TEXT NOT NULL,
				revoked_at TEXT,
				policy_digest TEXT NOT NULL DEFAULT ''
			);
			CREATE INDEX capability_grants_by_subject_task_kind
				ON capability_grants(subject, task_id, kind, resource);
			CREATE INDEX capability_grants_by_expiry
				ON capability_grants(expires_at, revoked_at);
		`); err != nil {
			return fmt.Errorf("apply schema version 13: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(13, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 13: %w", err)
		}
		version = 13
	}
	if version == 13 {
		if _, err := tx.ExecContext(ctx, `
			ALTER TABLE capability_grants ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT '';
			CREATE UNIQUE INDEX capability_grants_by_idempotency
				ON capability_grants(idempotency_key) WHERE idempotency_key <> '';
		`); err != nil {
			return fmt.Errorf("apply schema version 14: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(14, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 14: %w", err)
		}
		version = 14
	}
	if version == 14 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE execution_cells (
				cell_id TEXT PRIMARY KEY,
				task_id TEXT NOT NULL,
				backend TEXT NOT NULL CHECK(backend IN ('native','bubblewrap')),
				workspace TEXT NOT NULL,
				spec_digest TEXT NOT NULL,
				state TEXT NOT NULL CHECK(state IN ('new','preparing','ready','running','stopping','destroyed','failed')),
				process_ref TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				destroyed_at TEXT,
				failure_reason TEXT NOT NULL DEFAULT ''
			);
			CREATE INDEX execution_cells_by_task ON execution_cells(task_id, cell_id);
			CREATE INDEX execution_cells_by_state ON execution_cells(state, updated_at, cell_id);
		`); err != nil {
			return fmt.Errorf("apply schema version 15: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(15, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 15: %w", err)
		}
		version = 15
	}
	if version < 16 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE role_bindings (
				binding_id TEXT PRIMARY KEY,
				principal_id TEXT NOT NULL REFERENCES agents(agent_id),
				role TEXT NOT NULL,
				scope_id TEXT NOT NULL,
				bound_by TEXT NOT NULL,
				bound_at TEXT NOT NULL,
				revoked_at TEXT,
				policy_digest TEXT NOT NULL CHECK(length(policy_digest) = 71)
			);
			CREATE INDEX role_bindings_by_principal_scope ON role_bindings(principal_id, scope_id, revoked_at);
			CREATE INDEX role_bindings_by_role ON role_bindings(role, revoked_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 16: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(16, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 16: %w", err)
		}
		version = 16
	}
	if version < 17 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE gate_decisions (
				decision_id TEXT PRIMARY KEY,
				gate_point TEXT NOT NULL CHECK(gate_point IN ('pre-execution','pre-commit','pre-push','pre-merge','pre-release')),
				subject TEXT NOT NULL,
				resource TEXT NOT NULL,
				allowed INTEGER NOT NULL CHECK(allowed IN (0,1)),
				checks_json TEXT NOT NULL,
				policy_ids_json TEXT NOT NULL,
				policy_digest TEXT NOT NULL CHECK(length(policy_digest) = 71),
				change_digest TEXT NOT NULL DEFAULT '' CHECK(change_digest = '' OR length(change_digest) = 71),
				created_at TEXT NOT NULL,
				consumed_at TEXT
			);
			CREATE INDEX gate_decisions_by_point_subject ON gate_decisions(gate_point, subject, created_at);
			CREATE INDEX gate_decisions_by_resource_change ON gate_decisions(resource, change_digest);
		`); err != nil {
			return fmt.Errorf("migrate schema version 17: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(17, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 17: %w", err)
		}
		version = 17
	}
	if version < 18 {
		if _, err := tx.ExecContext(ctx, `
			ALTER TABLE gate_decisions ADD COLUMN state TEXT NOT NULL DEFAULT 'requested'
				CHECK(state IN ('requested','evaluating','allowed','denied','blocked','consumed','invalidated'));
		`); err != nil {
			return fmt.Errorf("migrate schema version 18: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(18, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 18: %w", err)
		}
		version = 18
	}
	if version < 19 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE secret_leases (
				lease_id TEXT PRIMARY KEY,
				subject TEXT NOT NULL,
				task_id TEXT NOT NULL REFERENCES tasks(task_id),
				provider TEXT NOT NULL,
				secret_name TEXT NOT NULL,
				secret_version TEXT NOT NULL,
				purpose TEXT NOT NULL,
				issued_at TEXT NOT NULL,
				expires_at TEXT NOT NULL,
				revoked_at TEXT,
				CHECK(expires_at > issued_at)
			);
			CREATE INDEX secret_leases_by_task_scope
				ON secret_leases(task_id, provider, secret_name, secret_version, purpose);
			CREATE INDEX secret_leases_by_expiry
				ON secret_leases(expires_at, revoked_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 19: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(19, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 19: %w", err)
		}
		version = 19
	}
	if version < 20 {
		if _, err := tx.ExecContext(ctx, `
			ALTER TABLE secret_leases ADD COLUMN state TEXT NOT NULL DEFAULT 'requested'
				CHECK(state IN ('requested','leased','used','revoked','expired'));
		`); err != nil {
			return fmt.Errorf("migrate schema version 20: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(20, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 20: %w", err)
		}
		version = 20
	}
	if version < 21 {
		if _, err := tx.ExecContext(ctx, `
			ALTER TABLE secret_leases ADD COLUMN access_owner TEXT NOT NULL DEFAULT '';
			ALTER TABLE secret_leases ADD COLUMN access_claimed_at TEXT NOT NULL DEFAULT '';
		`); err != nil {
			return fmt.Errorf("migrate schema version 21: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(21, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 21: %w", err)
		}
		version = 21
	}
	if version < 22 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE risk_assessments (
				assessment_id TEXT PRIMARY KEY,
				action_digest TEXT NOT NULL,
				level TEXT NOT NULL CHECK(level IN ('low','medium','high','critical')),
				score INTEGER NOT NULL CHECK(score >= 0),
				factors_json TEXT NOT NULL,
				requirements_json TEXT NOT NULL,
				policy_digest TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL
			);
			CREATE INDEX risk_assessments_by_action ON risk_assessments(action_digest);
			CREATE INDEX risk_assessments_by_created ON risk_assessments(created_at, assessment_id);
		`); err != nil {
			return fmt.Errorf("migrate schema version 22: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(22, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 22: %w", err)
		}
		version = 22
	}
	if version < 23 {
		if _, err := tx.ExecContext(ctx, `
			ALTER TABLE risk_assessments ADD COLUMN state TEXT NOT NULL DEFAULT 'requested'
				CHECK(state IN ('requested','classified','requirements_emitted'));
		`); err != nil {
			return fmt.Errorf("migrate schema version 23: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(23, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 23: %w", err)
		}
		version = 23
	}
	if version < 24 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE egress_decisions (
				decision_id TEXT PRIMARY KEY,
				idempotency_key TEXT NOT NULL UNIQUE,
				request_json TEXT NOT NULL,
				decision_json TEXT NOT NULL,
				created_at TEXT NOT NULL
			);
			CREATE INDEX egress_decisions_by_created ON egress_decisions(created_at, decision_id);
		`); err != nil {
			return fmt.Errorf("migrate schema version 24: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(24, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 24: %w", err)
		}
		version = 24
	}
	if version < 25 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE trusted_content_segments (
				segment_id TEXT PRIMARY KEY,
				idempotency_key TEXT NOT NULL UNIQUE,
				source_id TEXT NOT NULL,
				zone TEXT NOT NULL CHECK(zone IN ('system','owner_policy','project_policy','trusted_tool','repository_data','web_data','untrusted_content')),
				digest TEXT NOT NULL,
				content_ref TEXT NOT NULL,
				state TEXT NOT NULL CHECK(state IN ('ingested','zoned','rendered')),
				created_at TEXT NOT NULL,
				CHECK(digest = content_ref)
			);
			CREATE INDEX trusted_content_segments_by_source ON trusted_content_segments(source_id, created_at, segment_id);
			CREATE INDEX trusted_content_segments_by_state ON trusted_content_segments(state, created_at, segment_id);
		`); err != nil {
			return fmt.Errorf("migrate schema version 25: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(25, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 25: %w", err)
		}
		version = 25
	}
	if version < 26 {
		// `handoffs` is a legacy free-form compatibility history. Typed
		// handoffs are deliberately separate so no legacy body is silently
		// reinterpreted as canonical state.
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE typed_handoffs (
				handoff_id TEXT PRIMARY KEY,
				idempotency_key TEXT NOT NULL UNIQUE,
				version INTEGER NOT NULL CHECK(version = 1),
				task_id TEXT NOT NULL REFERENCES tasks(task_id),
				sender_principal TEXT NOT NULL,
				target_role TEXT NOT NULL CHECK(target_role IN ('orchestrator','architect','developer','qa','appsec')),
				status TEXT NOT NULL CHECK(status IN ('created','validated','accepted','rejected','consumed')),
				refs_json TEXT NOT NULL,
				context_digest TEXT NOT NULL CHECK(length(context_digest) = 71),
				created_at TEXT NOT NULL,
				consumed_at TEXT
			);
			CREATE INDEX typed_handoffs_by_task_status
				ON typed_handoffs(task_id, status, created_at, handoff_id);
			CREATE INDEX typed_handoffs_by_sender
				ON typed_handoffs(sender_principal, created_at, handoff_id);
		`); err != nil {
			return fmt.Errorf("migrate schema version 26: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(26, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 26: %w", err)
		}
		version = 26
	}
	if version < 27 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE verification_attestations (
				attestation_id TEXT PRIMARY KEY,
				change_id TEXT NOT NULL,
				principal TEXT NOT NULL,
				provider TEXT NOT NULL,
				role TEXT NOT NULL,
				kind TEXT NOT NULL,
				verdict TEXT NOT NULL CHECK(verdict IN ('PASS','FAIL','VETO')),
				evidence_id TEXT NOT NULL REFERENCES evidence_nodes(node_id),
				content_digest TEXT NOT NULL,
				created_at TEXT NOT NULL,
				invalidated_at TEXT
			);
			CREATE INDEX verification_attestations_by_change
				ON verification_attestations(change_id, kind, created_at, attestation_id);
			CREATE INDEX verification_attestations_by_principal
				ON verification_attestations(principal, change_id, kind, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 27: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(27, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 27: %w", err)
		}
		version = 27
	}
	if version < 28 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE provenance_records (
				change_id TEXT PRIMARY KEY,
				task_id TEXT NOT NULL,
				agent_id TEXT NOT NULL,
				provider TEXT NOT NULL,
				context_digest TEXT NOT NULL,
				patch_digest TEXT NOT NULL,
				commit_sha TEXT NOT NULL DEFAULT '',
				tool_call_ids TEXT NOT NULL DEFAULT '',
				evidence_ids TEXT NOT NULL DEFAULT '',
				approval_ids TEXT NOT NULL DEFAULT '',
				sealed INTEGER NOT NULL DEFAULT 0,
				created_at TEXT NOT NULL,
				sealed_at TEXT NOT NULL DEFAULT ''
			);
			CREATE INDEX provenance_records_by_task
				ON provenance_records(task_id, created_at);
			CREATE INDEX provenance_records_by_agent
				ON provenance_records(agent_id, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 28: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(28, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 28: %w", err)
		}
		version = 28
	}
	if version < 29 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE persistent_agent_memory (
				entry_id TEXT PRIMARY KEY,
				scope TEXT NOT NULL,
				scope_id TEXT NOT NULL,
				kind TEXT NOT NULL,
				text TEXT NOT NULL,
				confidence REAL NOT NULL DEFAULT 1.0,
				source_evidence_ids TEXT NOT NULL DEFAULT '',
				superseded_by TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			);
			CREATE INDEX persistent_agent_memory_by_scope
				ON persistent_agent_memory(scope, scope_id, created_at);
			CREATE INDEX persistent_agent_memory_by_kind
				ON persistent_agent_memory(kind, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 29: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(29, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 29: %w", err)
		}
		version = 29
	}
	if version < 30 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE code_ownership_leases (
				lease_id TEXT PRIMARY KEY,
				task_id TEXT NOT NULL,
				agent_id TEXT NOT NULL,
				targets_json TEXT NOT NULL,
				expires_at TEXT NOT NULL,
				created_at TEXT NOT NULL
			);
			CREATE INDEX code_ownership_leases_by_task
				ON code_ownership_leases(task_id, expires_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 30: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(30, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 30: %w", err)
		}
		version = 30
	}
	if version < 31 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE resource_accounting (
				record_id TEXT PRIMARY KEY,
				run_id TEXT NOT NULL,
				task_id TEXT NOT NULL,
				agent_id TEXT NOT NULL,
				provider TEXT NOT NULL,
				model TEXT NOT NULL,
				input_tokens INTEGER NOT NULL DEFAULT 0,
				output_tokens INTEGER NOT NULL DEFAULT 0,
				cost_micros INTEGER NOT NULL DEFAULT 0,
				wall_nanoseconds INTEGER NOT NULL DEFAULT 0,
				cpu_seconds REAL NOT NULL DEFAULT 0.0,
				peak_memory_bytes INTEGER NOT NULL DEFAULT 0,
				retry INTEGER NOT NULL DEFAULT 0,
				currency TEXT NOT NULL DEFAULT 'USD',
				created_at TEXT NOT NULL
			);
			CREATE INDEX resource_accounting_by_task
				ON resource_accounting(task_id, created_at);
			CREATE INDEX resource_accounting_by_provider
				ON resource_accounting(provider, model, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 31: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(31, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 31: %w", err)
		}
		version = 31
	}
	if version < 32 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE decision_records (
				decision_id TEXT PRIMARY KEY,
				task_id TEXT NOT NULL,
				agent_id TEXT NOT NULL,
				title TEXT NOT NULL,
				context TEXT NOT NULL,
				decision TEXT NOT NULL,
				consequences TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL,
				superseded_by TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			);
			CREATE INDEX decision_records_by_task
				ON decision_records(task_id, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 32: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(32, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 32: %w", err)
		}
		version = 32
	}
	if version < 33 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE failure_memory_records (
				failure_id TEXT PRIMARY KEY,
				scope_id TEXT NOT NULL,
				task_type TEXT NOT NULL,
				approach TEXT NOT NULL,
				root_cause TEXT NOT NULL,
				resolution TEXT NOT NULL,
				signature TEXT NOT NULL,
				evidence_ids TEXT NOT NULL DEFAULT '',
				severity TEXT NOT NULL DEFAULT 'MEDIUM',
				status TEXT NOT NULL DEFAULT 'ACTIVE',
				created_at TEXT NOT NULL
			);
			CREATE INDEX failure_memory_records_by_signature
				ON failure_memory_records(signature, task_type);
		`); err != nil {
			return fmt.Errorf("migrate schema version 33: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(33, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 33: %w", err)
		}
		version = 33
	}
	if version < 34 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE agent_checkpoints (
				checkpoint_id TEXT PRIMARY KEY,
				task_id TEXT NOT NULL,
				agent_id TEXT NOT NULL,
				change_id TEXT NOT NULL,
				context_digest TEXT NOT NULL,
				open_files_json TEXT NOT NULL DEFAULT '[]',
				evidence_ids_json TEXT NOT NULL DEFAULT '[]',
				outstanding INTEGER NOT NULL DEFAULT 0,
				created_at TEXT NOT NULL,
				version INTEGER NOT NULL DEFAULT 1
			);
			CREATE INDEX agent_checkpoints_by_task
				ON agent_checkpoints(task_id, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 34: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(34, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 34: %w", err)
		}
		version = 34
	}
	if version < 35 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE simulation_records (
				simulation_id TEXT PRIMARY KEY,
				tool TEXT NOT NULL,
				command TEXT NOT NULL,
				is_destructive INTEGER NOT NULL DEFAULT 0,
				files_deleted_json TEXT NOT NULL DEFAULT '[]',
				confidence REAL NOT NULL DEFAULT 1.0,
				created_at TEXT NOT NULL
			);
			CREATE INDEX simulation_records_by_command
				ON simulation_records(command, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 35: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(35, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 35: %w", err)
		}
		version = 35
	}
	if version < 36 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE compiled_contexts (
				context_id TEXT PRIMARY KEY,
				task_id TEXT NOT NULL,
				agent_id TEXT NOT NULL,
				prompt_text TEXT NOT NULL,
				token_count INTEGER NOT NULL DEFAULT 0,
				budget_limit INTEGER NOT NULL DEFAULT 0,
				created_at TEXT NOT NULL
			);
			CREATE INDEX compiled_contexts_by_task
				ON compiled_contexts(task_id, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 36: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(36, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 36: %w", err)
		}
		version = 36
	}
	if version < 37 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE cascade_evaluations (
				evaluation_id TEXT PRIMARY KEY,
				change_id TEXT NOT NULL,
				stage_name TEXT NOT NULL,
				status TEXT NOT NULL,
				evidence_id TEXT NOT NULL DEFAULT '',
				stop_reason TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL
			);
			CREATE INDEX cascade_evaluations_by_change
				ON cascade_evaluations(change_id, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 37: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(37, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 37: %w", err)
		}
		version = 37
	}
	if version < 38 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE mcp_gateway_logs (
				log_id TEXT PRIMARY KEY,
				subject TEXT NOT NULL,
				task_id TEXT NOT NULL,
				server TEXT NOT NULL,
				tool TEXT NOT NULL,
				allowed INTEGER NOT NULL DEFAULT 1,
				evidence_id TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL
			);
			CREATE INDEX mcp_gateway_logs_by_task
				ON mcp_gateway_logs(task_id, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 38: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(38, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 38: %w", err)
		}
		version = 38
	}
	if version < 39 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE conflict_predictions (
				prediction_id TEXT PRIMARY KEY,
				task_a TEXT NOT NULL,
				task_b TEXT NOT NULL,
				conflict_level TEXT NOT NULL,
				overlap_files_json TEXT NOT NULL DEFAULT '[]',
				created_at TEXT NOT NULL
			);
			CREATE INDEX conflict_predictions_by_tasks
				ON conflict_predictions(task_a, task_b);
		`); err != nil {
			return fmt.Errorf("migrate schema version 39: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(39, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 39: %w", err)
		}
		version = 39
	}
	if version < 40 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE context_budget_decisions (
				decision_id TEXT PRIMARY KEY,
				max_tokens INTEGER NOT NULL,
				action TEXT NOT NULL,
				estimated_tokens INTEGER NOT NULL,
				created_at TEXT NOT NULL
			);
			CREATE INDEX context_budget_decisions_by_action
				ON context_budget_decisions(action, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 40: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(40, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 40: %w", err)
		}
		version = 40
	}
	if version < 41 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE reproducible_replay_runs (
				run_id TEXT PRIMARY KEY,
				commit_sha TEXT NOT NULL,
				policy_digest TEXT NOT NULL,
				success INTEGER NOT NULL DEFAULT 1,
				evidence_id TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL
			);
			CREATE INDEX reproducible_replay_runs_by_commit
				ON reproducible_replay_runs(commit_sha, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 41: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(41, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 41: %w", err)
		}
		version = 41
	}
	if version < 42 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE audit_timeline_items (
				sequence INTEGER PRIMARY KEY AUTOINCREMENT,
				event_type TEXT NOT NULL,
				subject TEXT NOT NULL,
				resource_id TEXT NOT NULL,
				evidence_id TEXT NOT NULL DEFAULT '',
				summary TEXT NOT NULL,
				created_at TEXT NOT NULL
			);
			CREATE INDEX audit_timeline_items_by_resource
				ON audit_timeline_items(resource_id, sequence);
		`); err != nil {
			return fmt.Errorf("migrate schema version 42: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(42, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 42: %w", err)
		}
		version = 42
	}
	if version < 43 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE agent_reputation_scores (
				score_id TEXT PRIMARY KEY,
				subject_id TEXT NOT NULL,
				domain TEXT NOT NULL,
				value REAL NOT NULL DEFAULT 0.0,
				confidence REAL NOT NULL DEFAULT 0.0,
				created_at TEXT NOT NULL
			);
			CREATE INDEX agent_reputation_scores_by_subject
				ON agent_reputation_scores(subject_id, domain);
		`); err != nil {
			return fmt.Errorf("migrate schema version 43: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(43, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 43: %w", err)
		}
		version = 43
	}
	if version < 44 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE scheduler_task_leases (
				lease_id TEXT PRIMARY KEY,
				task_id TEXT NOT NULL,
				agent_id TEXT NOT NULL,
				status TEXT NOT NULL DEFAULT 'ACTIVE',
				created_at TEXT NOT NULL
			);
			CREATE INDEX scheduler_task_leases_by_task
				ON scheduler_task_leases(task_id, status);
		`); err != nil {
			return fmt.Errorf("migrate schema version 44: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(44, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 44: %w", err)
		}
		version = 44
	}
	if version < 45 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE security_dashboard_snapshots (
				snapshot_id TEXT PRIMARY KEY,
				denied_actions INTEGER NOT NULL DEFAULT 0,
				secret_violations INTEGER NOT NULL DEFAULT 0,
				critical_commands INTEGER NOT NULL DEFAULT 0,
				created_at TEXT NOT NULL
			);
			CREATE INDEX security_dashboard_snapshots_by_created
				ON security_dashboard_snapshots(created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 45: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(45, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 45: %w", err)
		}
		version = 45
	}
	if version < 46 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE evidence_confidence_evaluations (
				eval_id TEXT PRIMARY KEY,
				score REAL NOT NULL DEFAULT 0.0,
				formula_version TEXT NOT NULL,
				created_at TEXT NOT NULL
			);
			CREATE INDEX evidence_confidence_evaluations_by_created
				ON evidence_confidence_evaluations(created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 46: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(46, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 46: %w", err)
		}
		version = 46
	}
	if version < 47 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE security_profile_assignments (
				assignment_id TEXT PRIMARY KEY,
				profile_name TEXT NOT NULL,
				digest TEXT NOT NULL,
				created_at TEXT NOT NULL
			);
			CREATE INDEX security_profile_assignments_by_name
				ON security_profile_assignments(profile_name);
		`); err != nil {
			return fmt.Errorf("migrate schema version 47: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(47, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 47: %w", err)
		}
		version = 47
	}
	if version < 48 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE model_router_decisions (
				decision_id TEXT PRIMARY KEY,
				provider TEXT NOT NULL,
				model TEXT NOT NULL,
				score REAL NOT NULL DEFAULT 0.0,
				created_at TEXT NOT NULL
			);
			CREATE INDEX model_router_decisions_by_provider
				ON model_router_decisions(provider, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 48: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(48, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 48: %w", err)
		}
		version = 48
	}
	if version < 49 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE reconciliation_runs (
				run_id TEXT PRIMARY KEY,
				actions_count INTEGER NOT NULL DEFAULT 0,
				status TEXT NOT NULL DEFAULT 'COMPLETED',
				created_at TEXT NOT NULL
			);
			CREATE INDEX reconciliation_runs_by_status
				ON reconciliation_runs(status, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 49: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(49, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 49: %w", err)
		}
		version = 49
	}
	if version < 50 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE security_reputation_evaluations (
				eval_id TEXT PRIMARY KEY,
				subject_id TEXT NOT NULL,
				value REAL NOT NULL DEFAULT 0.0,
				confidence REAL NOT NULL DEFAULT 0.0,
				created_at TEXT NOT NULL
			);
			CREATE INDEX security_reputation_evaluations_by_subject
				ON security_reputation_evaluations(subject_id, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 50: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(50, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 50: %w", err)
		}
		version = 50
	}
	if version < 51 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE tui_session_snapshots (
				session_id TEXT PRIMARY KEY,
				agents_count INTEGER NOT NULL DEFAULT 0,
				tasks_count INTEGER NOT NULL DEFAULT 0,
				created_at TEXT NOT NULL
			);
			CREATE INDEX tui_session_snapshots_by_created
				ON tui_session_snapshots(created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 51: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(51, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 51: %w", err)
		}
		version = 51
	}
	if version < 52 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE agent_auto_recovery_events (
				event_id TEXT PRIMARY KEY,
				task_id TEXT NOT NULL,
				checkpoint_id TEXT NOT NULL,
				status TEXT NOT NULL DEFAULT 'RECOVERED',
				created_at TEXT NOT NULL
			);
			CREATE INDEX agent_auto_recovery_events_by_task
				ON agent_auto_recovery_events(task_id, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 52: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(52, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 52: %w", err)
		}
		version = 52
	}
	if version < 53 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE cross_model_reviews (
				review_id TEXT PRIMARY KEY,
				change_id TEXT NOT NULL,
				verifier_id TEXT NOT NULL,
				verdict TEXT NOT NULL DEFAULT 'PASSED',
				created_at TEXT NOT NULL
			);
			CREATE INDEX cross_model_reviews_by_change
				ON cross_model_reviews(change_id, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 53: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(53, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 53: %w", err)
		}
		version = 53
	}
	if version < 54 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE mcp_a2a_runtime_sessions (
				session_id TEXT PRIMARY KEY,
				protocol TEXT NOT NULL,
				principal TEXT NOT NULL,
				status TEXT NOT NULL DEFAULT 'ACTIVE',
				created_at TEXT NOT NULL
			);
			CREATE INDEX mcp_a2a_runtime_sessions_by_proto
				ON mcp_a2a_runtime_sessions(protocol, status);
		`); err != nil {
			return fmt.Errorf("migrate schema version 54: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(54, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 54: %w", err)
		}
		version = 54
	}
	if version < 55 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE distributed_nodes (
				node_id TEXT PRIMARY KEY,
				os TEXT NOT NULL,
				arch TEXT NOT NULL,
				attestation_id TEXT NOT NULL,
				status TEXT NOT NULL DEFAULT 'ACTIVE',
				created_at TEXT NOT NULL
			);
			CREATE INDEX distributed_nodes_by_status
				ON distributed_nodes(status, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 55: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(55, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 55: %w", err)
		}
		version = 55
	}
	if version < 56 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE remote_worker_attestations (
				attestation_id TEXT PRIMARY KEY,
				node_id TEXT NOT NULL,
				trusted INTEGER NOT NULL DEFAULT 1,
				created_at TEXT NOT NULL
			);
			CREATE INDEX remote_worker_attestations_by_node
				ON remote_worker_attestations(node_id, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 56: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(56, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 56: %w", err)
		}
		version = 56
	}
	if version < 57 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE scheduler_explanations (
				explanation_id TEXT PRIMARY KEY,
				task_id TEXT NOT NULL,
				selected_worker TEXT NOT NULL,
				created_at TEXT NOT NULL
			);
			CREATE INDEX scheduler_explanations_by_task
				ON scheduler_explanations(task_id, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 57: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(57, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 57: %w", err)
		}
		version = 57
	}
	if version < 58 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE research_reports (
				report_id TEXT PRIMARY KEY,
				question TEXT NOT NULL,
				digest TEXT NOT NULL,
				created_at TEXT NOT NULL
			);
			CREATE INDEX research_reports_by_question
				ON research_reports(question, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 58: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(58, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 58: %w", err)
		}
		version = 58
	}
	if version < 59 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE chaos_conformance_runs (
				run_id TEXT PRIMARY KEY,
				scenario_name TEXT NOT NULL,
				passed INTEGER NOT NULL DEFAULT 1,
				created_at TEXT NOT NULL
			);
			CREATE INDEX chaos_conformance_runs_by_scenario
				ON chaos_conformance_runs(scenario_name, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 59: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(59, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 59: %w", err)
		}
		version = 59
	}
	if version < 60 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE self_improvement_recommendations (
				recommendation_id TEXT PRIMARY KEY,
				kind TEXT NOT NULL,
				target TEXT NOT NULL,
				status TEXT NOT NULL DEFAULT 'PENDING',
				created_at TEXT NOT NULL
			);
			CREATE INDEX self_improvement_recommendations_by_kind
				ON self_improvement_recommendations(kind, status);
		`); err != nil {
			return fmt.Errorf("migrate schema version 60: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(60, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 60: %w", err)
		}
		version = 60
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
