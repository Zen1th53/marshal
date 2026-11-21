package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

const CommunitySchemaVersion = 91

// ArchitectureReceiverSchemaVersion is the last canonical step of the
// architecture receiver storage (104-107).
const ArchitectureReceiverSchemaVersion = schemaArchitectureOwnerNamespaces
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
	if !SchemaVersionSupported(version) {
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
	// Schema versions 27-37 and 39-67 each created one table (verification
	// attestations, provenance, agent memory, scheduler, security, tournament
	// and similar records) that no product code ever read or wrote. New
	// databases no longer create them, but every version is still recorded in
	// order so the ledger and version checks are unchanged. Databases that
	// already hold those tables keep them untouched.
	recordRetiredSchemaVersions := func(through int) error {
		for version < through {
			next := version + 1
			if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)", next, utcNow()); err != nil {
				return fmt.Errorf("record schema version %d: %w", next, err)
			}
			version = next
		}
		return nil
	}
	if err := recordRetiredSchemaVersions(37); err != nil {
		return err
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
	if err := recordRetiredSchemaVersions(67); err != nil {
		return err
	}
	if version < 68 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS memory_records_v2 (
				memory_id    TEXT PRIMARY KEY,
				project_id   TEXT NOT NULL REFERENCES projects(project_id),
				kind         TEXT NOT NULL,
				lifecycle    TEXT NOT NULL,
				confidence   TEXT NOT NULL DEFAULT '',
				authority    TEXT NOT NULL,
				title        TEXT NOT NULL DEFAULT '',
				body         TEXT NOT NULL,
				content_digest TEXT NOT NULL DEFAULT '',
				scope        TEXT NOT NULL,
				scope_id     TEXT NOT NULL,
				source_json  TEXT NOT NULL DEFAULT '{}',
				evidence_ids TEXT NOT NULL DEFAULT '',
				head_commit  TEXT NOT NULL DEFAULT '',
				branch_name  TEXT NOT NULL DEFAULT '',
				worktree_id  TEXT NOT NULL DEFAULT '',
				session_id   TEXT NOT NULL DEFAULT '',
				run_id       TEXT NOT NULL DEFAULT '',
				observed_at  TEXT NOT NULL,
				ingested_at  TEXT NOT NULL,
				valid_from   TEXT NOT NULL,
				valid_to     TEXT,
				last_verified_at TEXT,
				revision     INTEGER NOT NULL DEFAULT 0,
				superseded_by TEXT NOT NULL DEFAULT '',
				supersedes   TEXT NOT NULL DEFAULT '',
				conflict_ids TEXT NOT NULL DEFAULT '',
				acl_scope    TEXT NOT NULL DEFAULT '',
				created_at   TEXT NOT NULL,
				updated_at   TEXT NOT NULL,
				ext_meta_json TEXT NOT NULL DEFAULT '{}'
			);
			CREATE INDEX IF NOT EXISTS memory_records_v2_by_project_kind
				ON memory_records_v2(project_id, kind, lifecycle, created_at);
			CREATE INDEX IF NOT EXISTS memory_records_v2_by_scope
				ON memory_records_v2(project_id, scope, scope_id, created_at);
			CREATE INDEX IF NOT EXISTS memory_records_v2_by_lifecycle
				ON memory_records_v2(project_id, lifecycle, updated_at);
			CREATE INDEX IF NOT EXISTS memory_records_v2_by_digest
				ON memory_records_v2(content_digest);
		`); err != nil {
			return fmt.Errorf("migrate schema version 68: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(68, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 68: %w", err)
		}
		version = 68
	}
	if version < 69 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS memory_outbox (
				event_id     TEXT PRIMARY KEY,
				project_id   TEXT NOT NULL REFERENCES projects(project_id),
				memory_id    TEXT NOT NULL,
				event_type   TEXT NOT NULL,
				payload_json TEXT NOT NULL,
				created_at   TEXT NOT NULL,
				processed_at TEXT
			);
			CREATE INDEX IF NOT EXISTS memory_outbox_by_unprocessed
				ON memory_outbox(project_id, processed_at, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 69: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(69, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 69: %w", err)
		}
		version = 69
	}
	if version < 70 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS memory_retrieval_receipts (
				receipt_id       TEXT PRIMARY KEY,
				project_id       TEXT NOT NULL REFERENCES projects(project_id),
				caller_id        TEXT NOT NULL,
				query_text       TEXT NOT NULL DEFAULT '',
				query_digest     TEXT NOT NULL DEFAULT '',
				allowed_scopes   TEXT NOT NULL DEFAULT '[]',
				current_head     TEXT NOT NULL DEFAULT '',
				current_branch   TEXT NOT NULL DEFAULT '',
				max_records      INTEGER NOT NULL DEFAULT 0,
				max_bytes        INTEGER NOT NULL DEFAULT 0,
				consumed_bytes   INTEGER NOT NULL DEFAULT 0,
					decisions_json   TEXT NOT NULL DEFAULT '[]',
					run_id           TEXT NOT NULL DEFAULT '',
					task_id          TEXT NOT NULL DEFAULT '',
					provider         TEXT NOT NULL DEFAULT '',
					denied_count     INTEGER NOT NULL DEFAULT 0,
					created_at       TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS memory_retrieval_receipts_by_project
				ON memory_retrieval_receipts(project_id, created_at);
			CREATE INDEX IF NOT EXISTS memory_retrieval_receipts_by_task
				ON memory_retrieval_receipts(project_id, task_id, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 70: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(70, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 70: %w", err)
		}
		version = 70
	}
	if version < 71 {
		columns := []struct{ name, statement string }{
			{"evidence_ids", "ALTER TABLE memory_retrieval_receipts ADD COLUMN evidence_ids TEXT NOT NULL DEFAULT '';"},
			{"outcome_memory_id", "ALTER TABLE memory_retrieval_receipts ADD COLUMN outcome_memory_id TEXT NOT NULL DEFAULT '';"},
			{"outcome_status", "ALTER TABLE memory_retrieval_receipts ADD COLUMN outcome_status TEXT NOT NULL DEFAULT '';"},
		}
		for _, column := range columns {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('memory_retrieval_receipts') WHERE name = ?`, column.name).Scan(&exists); err != nil {
				return fmt.Errorf("inspect schema version 71 column %s: %w", column.name, err)
			}
			if exists > 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, column.statement); err != nil {
				return fmt.Errorf("migrate schema version 71: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(71, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 71: %w", err)
		}
		version = 71
	}
	if version < 72 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS task_memory_event_heads (
				task_id     TEXT PRIMARY KEY,
				project_id  TEXT NOT NULL REFERENCES projects(project_id),
				latest_seq  INTEGER NOT NULL DEFAULT 0 CHECK(latest_seq >= 0)
			);
			CREATE TABLE IF NOT EXISTS task_memory_events (
				task_id     TEXT NOT NULL,
				sequence    INTEGER NOT NULL CHECK(sequence > 0),
				project_id  TEXT NOT NULL REFERENCES projects(project_id),
				event_type  TEXT NOT NULL CHECK(event_type IN (
					'SLOT_CREATED','SLOT_UPDATED','SLOT_TOMBSTONED',
					'CANDIDATE_CREATED','MEMORY_PROMOTED','MEMORY_SUPERSEDED',
					'CONFLICT_CREATED','CONFLICT_RESOLVED','GRANT_REVOKED'
				)),
				priority    TEXT NOT NULL CHECK(priority IN ('LOW','MEDIUM','HIGH','CRITICAL')),
				memory_id   TEXT NOT NULL DEFAULT '',
				created_at  TEXT NOT NULL,
				PRIMARY KEY(task_id, sequence)
			);
			CREATE INDEX IF NOT EXISTS task_memory_events_by_project_task
				ON task_memory_events(project_id, task_id, sequence);

			CREATE TRIGGER IF NOT EXISTS task_memory_events_after_memory_insert
			AFTER INSERT ON memory_records_v2
			WHEN NEW.scope = 'task'
			 AND NEW.acl_scope = ''
			 AND (
				json_extract(NEW.ext_meta_json, '$.record_type') IN ('task_slot', 'task_slot_conflict')
				OR NEW.lifecycle IN ('candidate', 'conflicted')
			 )
			BEGIN
				INSERT INTO task_memory_event_heads(task_id, project_id, latest_seq)
				VALUES(NEW.scope_id, NEW.project_id, 1)
				ON CONFLICT(task_id) DO UPDATE SET latest_seq = latest_seq + 1;
				INSERT INTO task_memory_events(task_id, sequence, project_id, event_type, priority, memory_id, created_at)
				SELECT NEW.scope_id, latest_seq, NEW.project_id,
					CASE
						WHEN NEW.lifecycle = 'conflicted' THEN 'CONFLICT_CREATED'
						WHEN NEW.lifecycle = 'candidate' THEN 'CANDIDATE_CREATED'
						ELSE 'SLOT_CREATED'
					END,
					CASE WHEN NEW.lifecycle = 'conflicted' THEN 'HIGH' ELSE 'MEDIUM' END,
					NEW.memory_id, NEW.updated_at
				FROM task_memory_event_heads WHERE task_id = NEW.scope_id;
				DELETE FROM task_memory_events
				WHERE task_id = NEW.scope_id AND sequence <= (
					SELECT latest_seq - 4096 FROM task_memory_event_heads WHERE task_id = NEW.scope_id
				);
			END;

			CREATE TRIGGER IF NOT EXISTS task_memory_events_after_memory_update
			AFTER UPDATE ON memory_records_v2
			WHEN NEW.scope = 'task'
			 AND NEW.acl_scope = ''
			 AND (
				json_extract(NEW.ext_meta_json, '$.record_type') = 'task_slot'
				OR NEW.lifecycle IN ('superseded', 'tombstoned', 'durable')
			 )
			BEGIN
				INSERT INTO task_memory_event_heads(task_id, project_id, latest_seq)
				VALUES(NEW.scope_id, NEW.project_id, 1)
				ON CONFLICT(task_id) DO UPDATE SET latest_seq = latest_seq + 1;
				INSERT INTO task_memory_events(task_id, sequence, project_id, event_type, priority, memory_id, created_at)
				SELECT NEW.scope_id, latest_seq, NEW.project_id,
					CASE
						WHEN NEW.lifecycle = 'tombstoned' THEN 'SLOT_TOMBSTONED'
						WHEN NEW.lifecycle = 'superseded' THEN 'MEMORY_SUPERSEDED'
						WHEN NEW.lifecycle = 'durable' THEN 'MEMORY_PROMOTED'
						ELSE 'SLOT_UPDATED'
					END,
					CASE
						WHEN NEW.lifecycle IN ('tombstoned', 'superseded') THEN 'HIGH'
						ELSE 'MEDIUM'
					END,
					NEW.memory_id, NEW.updated_at
				FROM task_memory_event_heads WHERE task_id = NEW.scope_id;
				DELETE FROM task_memory_events
				WHERE task_id = NEW.scope_id AND sequence <= (
					SELECT latest_seq - 4096 FROM task_memory_event_heads WHERE task_id = NEW.scope_id
				);
			END;
		`); err != nil {
			return fmt.Errorf("migrate schema version 72: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(72, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 72: %w", err)
		}
		version = 72
	}
	if version < 73 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS goal_contracts (
				goal_id                   TEXT NOT NULL,
				session_id                TEXT NOT NULL,
				revision                  INTEGER NOT NULL CHECK(revision >= 1),
				desired_outcome           TEXT NOT NULL,
				expected_artifact         TEXT NOT NULL,
				scope_json                TEXT NOT NULL DEFAULT '[]',
				constraints_json          TEXT NOT NULL DEFAULT '[]',
				do_not_do_json            TEXT NOT NULL DEFAULT '[]',
				success_criteria_json     TEXT NOT NULL DEFAULT '[]',
				risk                      TEXT NOT NULL CHECK(risk IN ('R0','R1','R2','R3')),
				authority_source          TEXT NOT NULL,
				budget_ref                TEXT NOT NULL DEFAULT '',
				critical_claims_json      TEXT NOT NULL DEFAULT '[]',
				understanding_state       TEXT NOT NULL CHECK(understanding_state IN ('READY','NEEDS_INPUT')),
				unresolved_decisions_json TEXT NOT NULL DEFAULT '[]',
				assumptions_json          TEXT NOT NULL DEFAULT '[]',
				repo_commit               TEXT NOT NULL DEFAULT '',
				created_at                TEXT NOT NULL,
				updated_at                TEXT NOT NULL,
				PRIMARY KEY(goal_id, revision)
			);
			CREATE INDEX IF NOT EXISTS idx_goal_contracts_session ON goal_contracts(session_id, revision);

			CREATE TABLE IF NOT EXISTS goal_active (
				session_id      TEXT PRIMARY KEY,
				active_goal_id  TEXT NOT NULL,
				active_revision INTEGER NOT NULL,
				updated_at      TEXT NOT NULL,
				FOREIGN KEY(active_goal_id, active_revision) REFERENCES goal_contracts(goal_id, revision)
			);
		`); err != nil {
			return fmt.Errorf("migrate schema version 73: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(73, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 73: %w", err)
		}
		version = 73
	}
	if version < 74 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS claims (
				claim_id                    TEXT PRIMARY KEY,
				goal_id                     TEXT NOT NULL,
				goal_revision               INTEGER NOT NULL,
				subject                     TEXT NOT NULL,
				normalized_text             TEXT NOT NULL,
				scope                       TEXT NOT NULL,
				criticality                 TEXT NOT NULL CHECK(criticality IN ('CRITICAL_BLOCKER', 'CRITICAL_FEATURE', 'STANDARD', 'INFORMATIONAL')),
				state                       TEXT NOT NULL CHECK(state IN ('UNSUPPORTED', 'SUPPORTED', 'VERIFIED', 'CONTESTED', 'STALE', 'INVALIDATED')),
				predecessor_id              TEXT NOT NULL DEFAULT '',
				supersedes_id               TEXT NOT NULL DEFAULT '',
				author_provenance_json      TEXT NOT NULL DEFAULT '{}',
				supporting_evidence_json    TEXT NOT NULL DEFAULT '[]',
				contradicting_evidence_json TEXT NOT NULL DEFAULT '[]',
				source_clusters_json        TEXT NOT NULL DEFAULT '[]',
				binding_json                TEXT NOT NULL DEFAULT '{}',
				state_reason                TEXT NOT NULL DEFAULT '',
				created_at                  TEXT NOT NULL,
				updated_at                  TEXT NOT NULL,
				evaluated_at                TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_claims_goal ON claims(goal_id, goal_revision);
			CREATE INDEX IF NOT EXISTS idx_claims_state ON claims(state);
			CREATE INDEX IF NOT EXISTS idx_claims_criticality ON claims(criticality);
			CREATE INDEX IF NOT EXISTS idx_claims_scope ON claims(scope);

			CREATE TABLE IF NOT EXISTS claim_transitions (
				transition_id         TEXT PRIMARY KEY,
				claim_id              TEXT NOT NULL REFERENCES claims(claim_id) ON DELETE CASCADE,
				from_state            TEXT NOT NULL CHECK(from_state IN ('UNSUPPORTED', 'SUPPORTED', 'VERIFIED', 'CONTESTED', 'STALE', 'INVALIDATED')),
				to_state              TEXT NOT NULL CHECK(to_state IN ('UNSUPPORTED', 'SUPPORTED', 'VERIFIED', 'CONTESTED', 'STALE', 'INVALIDATED')),
				reason                TEXT NOT NULL,
				actor_provenance_json TEXT NOT NULL DEFAULT '{}',
				evidence_ref_json     TEXT NOT NULL DEFAULT '{}',
				timestamp             TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_claim_transitions_claim_id ON claim_transitions(claim_id, timestamp);
		`); err != nil {
			return fmt.Errorf("migrate schema version 74: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(74, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 74: %w", err)
		}
		version = 74
	}
	if version < 75 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS handoff_checkpoints (
				checkpoint_id           TEXT PRIMARY KEY,
				version                 INTEGER NOT NULL DEFAULT 1,
				goal_id                 TEXT NOT NULL,
				goal_revision           INTEGER NOT NULL,
				constraints_digest      TEXT NOT NULL,
				task_id                 TEXT NOT NULL,
				session_id              TEXT NOT NULL,
				handoff_id              TEXT NOT NULL DEFAULT '',
				agent_id                TEXT NOT NULL,
				harness                 TEXT NOT NULL,
				model                   TEXT NOT NULL DEFAULT '',
				role                    TEXT NOT NULL,
				branch                  TEXT NOT NULL DEFAULT '',
				worktree_path           TEXT NOT NULL DEFAULT '',
				base_head               TEXT NOT NULL DEFAULT '',
				result_head             TEXT NOT NULL DEFAULT '',
				tree_sha                TEXT NOT NULL DEFAULT '',
				diff_digest             TEXT NOT NULL DEFAULT '',
				last_consumed_cursor    TEXT NOT NULL DEFAULT '',
				task_slots_json         TEXT NOT NULL DEFAULT '{}',
				claims_json             TEXT NOT NULL DEFAULT '[]',
				evidence_refs_json      TEXT NOT NULL DEFAULT '[]',
				budget_state_json       TEXT NOT NULL DEFAULT '{}',
				pending_blockers_json   TEXT NOT NULL DEFAULT '[]',
				state_snapshot_json     TEXT NOT NULL DEFAULT '{}',
				reason                  TEXT NOT NULL DEFAULT '',
				created_at              TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_checkpoints_task ON handoff_checkpoints(task_id, created_at);
			CREATE INDEX IF NOT EXISTS idx_checkpoints_session ON handoff_checkpoints(session_id, created_at);
			CREATE INDEX IF NOT EXISTS idx_checkpoints_handoff ON handoff_checkpoints(handoff_id);

			CREATE TABLE IF NOT EXISTS checkpoint_rollbacks (
				rollback_id                TEXT PRIMARY KEY,
				checkpoint_id              TEXT NOT NULL REFERENCES handoff_checkpoints(checkpoint_id),
				from_checkpoint_id         TEXT NOT NULL DEFAULT '',
				reason                     TEXT NOT NULL,
				actor_provenance_json      TEXT NOT NULL DEFAULT '{}',
				invalidated_claim_ids_json TEXT NOT NULL DEFAULT '[]',
				created_at                 TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_rollbacks_ckpt ON checkpoint_rollbacks(checkpoint_id);
		`); err != nil {
			return fmt.Errorf("migrate schema version 75: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(75, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 75: %w", err)
		}
		version = 75
	}
	if version < 76 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS goal_terminations (
				session_id           TEXT NOT NULL,
				goal_id              TEXT NOT NULL,
				revision             INTEGER NOT NULL,
				state                TEXT NOT NULL CHECK(state IN ('SUCCESS', 'PARTIAL', 'BLOCKED', 'BUDGET_EXHAUSTED', 'CANCELLED')),
				reason_code          TEXT NOT NULL,
				reason_detail        TEXT NOT NULL DEFAULT '',
				budget_consumed_json TEXT NOT NULL DEFAULT '{}',
				checkpoint_id        TEXT NOT NULL DEFAULT '',
				completed_at         TEXT NOT NULL,
				PRIMARY KEY(session_id, goal_id, revision)
			);
			CREATE INDEX IF NOT EXISTS idx_terminations_goal ON goal_terminations(goal_id, revision);

			CREATE TABLE IF NOT EXISTS budget_trackers (
				session_id           TEXT NOT NULL,
				goal_id              TEXT NOT NULL,
				revision             INTEGER NOT NULL,
				consumed_json        TEXT NOT NULL DEFAULT '{}',
				updated_at           TEXT NOT NULL,
				PRIMARY KEY(session_id, goal_id, revision)
			);
		`); err != nil {
			return fmt.Errorf("migrate schema version 76: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(76, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 76: %w", err)
		}
		version = 76
	}
	if version < 77 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS blind_interpretations (
				interpretation_id TEXT PRIMARY KEY,
				goal_id           TEXT NOT NULL,
				goal_revision     INTEGER NOT NULL,
				session_id        TEXT NOT NULL,
				agent_id          TEXT NOT NULL,
				harness           TEXT NOT NULL,
				model             TEXT NOT NULL DEFAULT '',
				desired_outcome   TEXT NOT NULL,
				expected_artifact TEXT NOT NULL,
				scope_json        TEXT NOT NULL DEFAULT '[]',
				constraints_json  TEXT NOT NULL DEFAULT '[]',
				assumptions_json  TEXT NOT NULL DEFAULT '[]',
				is_destructive    INTEGER NOT NULL DEFAULT 0,
				payload_json      TEXT NOT NULL DEFAULT '{}',
				submitted_at      TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_blind_interp_goal ON blind_interpretations(goal_id, goal_revision);
			CREATE INDEX IF NOT EXISTS idx_blind_interp_session ON blind_interpretations(session_id, submitted_at);

			CREATE TABLE IF NOT EXISTS interpretation_resolutions (
				resolution_id       TEXT PRIMARY KEY,
				goal_id             TEXT NOT NULL,
				goal_revision       INTEGER NOT NULL,
				session_id          TEXT NOT NULL,
				state               TEXT NOT NULL CHECK(state IN ('READY', 'NEEDS_INPUT')),
				required_count      INTEGER NOT NULL DEFAULT 1,
				collected_count     INTEGER NOT NULL DEFAULT 1,
				consensus_confirmed INTEGER NOT NULL DEFAULT 0,
				divergences_json    TEXT NOT NULL DEFAULT '[]',
				questions_json      TEXT NOT NULL DEFAULT '[]',
				message             TEXT NOT NULL DEFAULT '',
				resolved_at         TEXT NOT NULL,
				UNIQUE(session_id, goal_id, goal_revision)
			);
			CREATE INDEX IF NOT EXISTS idx_interp_res_goal ON interpretation_resolutions(goal_id, goal_revision);
		`); err != nil {
			return fmt.Errorf("migrate schema version 77: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(77, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 77: %w", err)
		}
		version = 77
	}
	if version < 78 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS team_sessions (
				session_id        TEXT PRIMARY KEY,
				goal_id           TEXT NOT NULL,
				goal_revision     INTEGER NOT NULL,
				active_turn       TEXT NOT NULL DEFAULT '',
				turn_sequence     INTEGER NOT NULL DEFAULT 0,
				status            TEXT NOT NULL DEFAULT 'ACTIVE',
				participants_json TEXT NOT NULL DEFAULT '[]',
				created_at        TEXT NOT NULL,
				updated_at        TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_team_sessions_goal ON team_sessions(goal_id, goal_revision);

			CREATE TABLE IF NOT EXISTS agent_messages (
				message_id        TEXT PRIMARY KEY,
				session_id        TEXT NOT NULL REFERENCES team_sessions(session_id),
				task_id           TEXT NOT NULL DEFAULT '',
				from_agent        TEXT NOT NULL,
				from_harness      TEXT NOT NULL DEFAULT '',
				from_model        TEXT NOT NULL DEFAULT '',
				to_agent          TEXT NOT NULL DEFAULT '',
				kind              TEXT NOT NULL,
				content           TEXT NOT NULL,
				claim_ids_json    TEXT NOT NULL DEFAULT '[]',
				evidence_ids_json TEXT NOT NULL DEFAULT '[]',
				created_at        TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_agent_messages_sess ON agent_messages(session_id, created_at);
			CREATE INDEX IF NOT EXISTS idx_agent_messages_task ON agent_messages(task_id, created_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 78: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(78, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 78: %w", err)
		}
		version = 78
	}
	if version < 79 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS harness_profiles (
				harness_name          TEXT PRIMARY KEY,
				installed_version     TEXT NOT NULL,
				binary_path           TEXT NOT NULL DEFAULT '',
				supported_models_json TEXT NOT NULL DEFAULT '[]',
				default_model         TEXT NOT NULL DEFAULT '',
				feature_support_json  TEXT NOT NULL DEFAULT '{}',
				reasoning_knobs_json  TEXT NOT NULL DEFAULT '[]',
				native_modes_json     TEXT NOT NULL DEFAULT '[]',
				probe_evidence_id     TEXT NOT NULL DEFAULT '',
				probed_at             TEXT NOT NULL,
				expires_at            TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_harness_profiles_version ON harness_profiles(harness_name, installed_version);
		`); err != nil {
			return fmt.Errorf("migrate schema version 79: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(79, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 79: %w", err)
		}
		version = 79
	}
	if version < 80 {
		// Process 00 constitutional state. Three tables, each holding truth
		// that cannot be reconstructed from anything else:
		//
		// session_constitutions binds a session to the exact constitutional
		// semantics it started under, so a restart or a runtime upgrade cannot
		// silently reinterpret work already done. The binding is immutable for
		// the life of the session, which is enforced by the absence of any
		// update path and by the primary key.
		//
		// constitutional_decisions is the audit record of every material
		// decision: what was asked, what the gate answered, and the binding
		// digest an approval must match. Keeping the digest here is what makes
		// approval replay detectable after the fact and not only at the gate.
		//
		// constitutional_violations records breaches and the response they
		// drew, so a suspended session cannot be quietly resumed.
		if _, err := tx.ExecContext(ctx, `
			-- session_id is intentionally not a foreign key to sessions.
			-- A constitution binding is established when a session of work
			-- begins, which is earlier and broader than an agent row in
			-- sessions: a CLI or Web session governed by Process 00 need not
			-- have registered an agent. Binding the constitution late, or only
			-- for sessions that happen to have an agent row, would leave
			-- ungoverned decisions at exactly the moment governance matters
			-- most. project_id remains a foreign key, because project
			-- isolation is the constraint that must hold.
			CREATE TABLE IF NOT EXISTS session_constitutions (
				session_id            TEXT PRIMARY KEY,
				project_id            TEXT NOT NULL REFERENCES projects(project_id),
				constitution_version  TEXT NOT NULL,
				invariant_digest      TEXT NOT NULL,
				mode                  TEXT NOT NULL CHECK(mode IN ('standard','ultra')),
				bound_at              TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_session_constitutions_project
				ON session_constitutions(project_id, constitution_version);

			CREATE TABLE IF NOT EXISTS constitutional_decisions (
				decision_id           TEXT PRIMARY KEY,
				project_id            TEXT NOT NULL REFERENCES projects(project_id),
				session_id            TEXT NOT NULL,
				constitution_version  TEXT NOT NULL,
				process               INTEGER NOT NULL CHECK(process BETWEEN 1 AND 8),
				domain                TEXT NOT NULL,
				action                TEXT NOT NULL,
				actor                 TEXT NOT NULL,
				surface               TEXT NOT NULL CHECK(surface IN ('tui','cli','web','mcp','a2a','core')),
				mode                  TEXT NOT NULL CHECK(mode IN ('standard','ultra')),
				outcome               TEXT NOT NULL CHECK(outcome IN (
					'ALLOW','BLOCK','REQUIRE_APPROVAL','DEGRADE','SUSPEND','REPLAN','REQUIRE_VERIFICATION'
				)),
				reason                TEXT NOT NULL,
				binding_digest        TEXT NOT NULL,
				state_digest          TEXT NOT NULL,
				advisory_status       TEXT NOT NULL CHECK(advisory_status IN ('ACCEPTED','DISCARDED','ABSENT')),
				findings_json         TEXT NOT NULL DEFAULT '[]',
				evaluated_at          TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_constitutional_decisions_session
				ON constitutional_decisions(session_id, evaluated_at);
			CREATE INDEX IF NOT EXISTS idx_constitutional_decisions_binding
				ON constitutional_decisions(project_id, binding_digest);
			CREATE INDEX IF NOT EXISTS idx_constitutional_decisions_outcome
				ON constitutional_decisions(project_id, outcome, evaluated_at);

			CREATE TABLE IF NOT EXISTS constitutional_violations (
				violation_id          TEXT PRIMARY KEY,
				project_id            TEXT NOT NULL REFERENCES projects(project_id),
				session_id            TEXT NOT NULL,
				decision_id           TEXT,
				violation_class       TEXT NOT NULL,
				invariant_id          TEXT NOT NULL,
				response              TEXT NOT NULL CHECK(response IN (
					'BLOCK','REVOKE','SUSPEND','INVALIDATE','REVIEW'
				)),
				actor                 TEXT NOT NULL DEFAULT '',
				surface               TEXT NOT NULL DEFAULT '',
				constitution_version  TEXT NOT NULL,
				detail                TEXT NOT NULL DEFAULT '',
				detected_at           TEXT NOT NULL,
				resolved_at           TEXT
			);
			CREATE INDEX IF NOT EXISTS idx_constitutional_violations_session
				ON constitutional_violations(session_id, detected_at);
			CREATE INDEX IF NOT EXISTS idx_constitutional_violations_open
				ON constitutional_violations(project_id, response) WHERE resolved_at IS NULL;
		`); err != nil {
			return fmt.Errorf("migrate schema version 80: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(80, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 80: %w", err)
		}
		version = 80
	}
	if version < 81 {
		// Process 03 goal intake.
		//
		// The intake fields are stored as one JSON column rather than as eight
		// separate columns. In raw SQLite the difference is fractions of a
		// millisecond, but each ALTER is its own driver round trip and the
		// migration chain is replayed by every store test — around 150 of
		// them. Measured: eight columns cost roughly 18ms per chain against
		// 3ms for one, which is the difference between the store package
		// fitting inside the race-detector timeout and exceeding it.
		//
		// project_id stays a real column because it is the one field worth
		// indexing and querying by: it binds a Goal to a project, which is how
		// Process 02 isolation reaches goal state.
		//
		// The payload holds the user's original request, its digest, the
		// constitution version, the confirmation state, the assessment, the
		// revision reason and whether a model contributed. They are always
		// read and written together, so separate columns would buy nothing and
		// cost measurably.
		var intakeColumns int
		if err := tx.QueryRowContext(ctx, `
			SELECT count(*) FROM pragma_table_info('goal_contracts')
			WHERE name IN ('project_id','intake_json')
		`).Scan(&intakeColumns); err != nil {
			return fmt.Errorf("inspect schema version 81 columns: %w", err)
		}
		// Both columns are added in one transaction, so the database holds
		// either both or neither. Anything else means the file was modified
		// outside MARSHAL, and refusing beats guessing.
		if intakeColumns != 0 && intakeColumns != 2 {
			return fmt.Errorf(
				"migrate schema version 81: goal_contracts has %d of 2 expected columns; the schema was modified outside MARSHAL",
				intakeColumns)
		}
		if intakeColumns == 0 {
			if _, err := tx.ExecContext(ctx, `
				ALTER TABLE goal_contracts ADD COLUMN project_id TEXT NOT NULL DEFAULT '';
				ALTER TABLE goal_contracts ADD COLUMN intake_json TEXT NOT NULL DEFAULT '{}';
				CREATE INDEX IF NOT EXISTS idx_goal_contracts_project ON goal_contracts(project_id, revision);
			`); err != nil {
				return fmt.Errorf("migrate schema version 81: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(81, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 81: %w", err)
		}
		version = 81
	}
	if version < 82 {
		// Process 04 execution plans.
		//
		// A new table rather than columns on an existing one: plans have their
		// own lifecycle and their own revision history, and every version of a
		// plan is kept so that what was approved stays inspectable after the
		// plan moves on. One CREATE TABLE is a single exec, so unlike the
		// per-column ALTERs of version 81 this costs the migration chain
		// almost nothing.
		//
		// (plan_id, version) is the primary key because plan versions are
		// immutable once written: a revision inserts a new row rather than
		// updating one, which is what makes provenance survive a restart.
		//
		// The plan body is JSON. Its shape belongs to internal/plan, it is
		// always read whole, and giving each field a column would freeze the
		// planning model into the schema — every planning change would then
		// need a migration, which is how the chain got expensive before.
		//
		// goal_id and goal_revision are real columns because staleness is
		// decided by comparing them against the live Goal, and that is a query
		// rather than a field read. Likewise project_id: it is how Process 02
		// isolation reaches plan state.
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS execution_plans (
				plan_id               TEXT NOT NULL,
				version               INTEGER NOT NULL,
				project_id            TEXT NOT NULL,
				goal_id               TEXT NOT NULL,
				goal_revision         INTEGER NOT NULL,
				state                 TEXT NOT NULL CHECK(state IN (
					'DRAFT','NEEDS_INPUT','READY','APPROVED','STALE','BLOCKED','CANCELLED'
				)),
				mode                  TEXT NOT NULL CHECK(mode IN ('standard','ultra')),
				constitution_version  TEXT NOT NULL,
				graph_digest          TEXT NOT NULL DEFAULT '',
				supersedes            INTEGER NOT NULL DEFAULT 0,
				revision_reason       TEXT NOT NULL DEFAULT '',
				plan_json             TEXT NOT NULL DEFAULT '{}',
				created_at            TEXT NOT NULL,
				PRIMARY KEY (plan_id, version)
			);
			CREATE INDEX IF NOT EXISTS idx_execution_plans_project
				ON execution_plans(project_id, created_at);
			CREATE INDEX IF NOT EXISTS idx_execution_plans_goal
				ON execution_plans(goal_id, goal_revision);

			CREATE TABLE IF NOT EXISTS execution_plan_active (
				project_id            TEXT PRIMARY KEY,
				plan_id               TEXT NOT NULL,
				version               INTEGER NOT NULL,
				updated_at            TEXT NOT NULL
			);
		`); err != nil {
			return fmt.Errorf("migrate schema version 82: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(82, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 82: %w", err)
		}
		version = 82
	}
	if version < 83 {
		// Process 06 review, verification, and completion. Sessions are mutable
		// only through CAS; terminal attestations are append-only records bound
		// to their exact Goal/Plan/Run/tree/environment state.
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS verification_sessions (
				verification_id TEXT PRIMARY KEY,
				version INTEGER NOT NULL CHECK(version >= 1),
				project_id TEXT NOT NULL,
				goal_id TEXT NOT NULL,
				plan_id TEXT NOT NULL,
				run_id TEXT NOT NULL,
				state TEXT NOT NULL CHECK(state IN ('VERIFIED_COMPLETE','PARTIALLY_SATISFIED','VERIFICATION_FAILED','BLOCKED','NEEDS_REEXECUTION','NEEDS_REPLAN','CANCELLED')),
				session_json TEXT NOT NULL,
				updated_at TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_verification_sessions_binding
				ON verification_sessions(project_id, goal_id, plan_id, run_id);
			CREATE TABLE IF NOT EXISTS completion_attestations (
				attestation_id TEXT PRIMARY KEY,
				verification_id TEXT NOT NULL,
				verification_version INTEGER NOT NULL,
				decision TEXT NOT NULL,
				digest TEXT NOT NULL UNIQUE,
				attestation_json TEXT NOT NULL,
				issued_at TEXT NOT NULL,
				FOREIGN KEY(verification_id) REFERENCES verification_sessions(verification_id)
			);
		`); err != nil {
			return fmt.Errorf("migrate schema version 83: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(83, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 83: %w", err)
		}
		version = 83
	}
	if version < 84 {
		// Process 07 learning, memory and adaptation. Memory commits are
		// append-only and digest-protected. Memory items are mutable only
		// through CAS on (item_id, version), so a stale writer cannot
		// overwrite fresher evidence and an invalidation cannot be lost.
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS memory_commits (
				memory_commit_id TEXT PRIMARY KEY,
				version INTEGER NOT NULL CHECK(version >= 1),
				project_id TEXT NOT NULL,
				goal_id TEXT NOT NULL,
				plan_id TEXT NOT NULL,
				run_id TEXT NOT NULL,
				verification_id TEXT NOT NULL,
				verification_version INTEGER NOT NULL,
				outcome TEXT NOT NULL CHECK(outcome IN ('VERIFIED_COMPLETE','PARTIAL','FAILED','BLOCKED')),
				attestation_digest TEXT NOT NULL,
				evidence_bundle_digest TEXT NOT NULL,
				digest TEXT NOT NULL UNIQUE,
				commit_json TEXT NOT NULL,
				created_at TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_memory_commits_binding
				ON memory_commits(project_id, goal_id, plan_id, run_id);
			CREATE INDEX IF NOT EXISTS idx_memory_commits_verification
				ON memory_commits(verification_id, verification_version);
			CREATE TABLE IF NOT EXISTS memory_items (
				item_id TEXT PRIMARY KEY,
				version INTEGER NOT NULL CHECK(version >= 1),
				scope TEXT NOT NULL CHECK(scope IN ('PROJECT','GENERAL')),
				project_id TEXT NOT NULL DEFAULT '',
				state TEXT NOT NULL CHECK(state IN ('UNSUPPORTED','SUPPORTED','VERIFIED','CONTESTED','STALE','INVALIDATED')),
				critical INTEGER NOT NULL CHECK(critical IN (0,1)),
				memory_commit_id TEXT NOT NULL,
				digest TEXT NOT NULL,
				item_json TEXT NOT NULL,
				recorded_at TEXT NOT NULL,
				expires_at TEXT,
				FOREIGN KEY(memory_commit_id) REFERENCES memory_commits(memory_commit_id)
			);
			CREATE INDEX IF NOT EXISTS idx_memory_items_scope
				ON memory_items(scope, project_id, state);
			CREATE TABLE IF NOT EXISTS memory_item_revisions (
				item_id TEXT NOT NULL,
				version INTEGER NOT NULL CHECK(version >= 1),
				state TEXT NOT NULL,
				memory_commit_id TEXT NOT NULL,
				digest TEXT NOT NULL,
				item_json TEXT NOT NULL,
				recorded_at TEXT NOT NULL,
				PRIMARY KEY(item_id, version)
			);
			CREATE TABLE IF NOT EXISTS memory_dependencies (
				item_id TEXT NOT NULL,
				kind TEXT NOT NULL,
				dependency_id TEXT NOT NULL,
				dependency_version TEXT NOT NULL,
				PRIMARY KEY(item_id, kind, dependency_id)
			);
			CREATE INDEX IF NOT EXISTS idx_memory_dependencies_lookup
				ON memory_dependencies(kind, dependency_id);
			CREATE TABLE IF NOT EXISTS memory_evidence (
				item_id TEXT NOT NULL,
				evidence_id TEXT NOT NULL,
				cluster_id TEXT NOT NULL,
				digest TEXT NOT NULL,
				kind TEXT NOT NULL,
				observed_at TEXT NOT NULL,
				PRIMARY KEY(item_id, evidence_id)
			);
			CREATE INDEX IF NOT EXISTS idx_memory_evidence_cluster
				ON memory_evidence(cluster_id);
			CREATE TABLE IF NOT EXISTS routing_observations (
				observation_id TEXT PRIMARY KEY,
				memory_commit_id TEXT NOT NULL,
				task_class TEXT NOT NULL,
				provider TEXT NOT NULL,
				provider_version TEXT NOT NULL,
				model TEXT NOT NULL,
				outcome TEXT NOT NULL CHECK(outcome IN ('VERIFIED_COMPLETE','PARTIAL','FAILED','BLOCKED')),
				selected INTEGER NOT NULL CHECK(selected IN (0,1)),
				cluster_id TEXT NOT NULL,
				observation_json TEXT NOT NULL,
				observed_at TEXT NOT NULL,
				FOREIGN KEY(memory_commit_id) REFERENCES memory_commits(memory_commit_id)
			);
			CREATE INDEX IF NOT EXISTS idx_routing_observations_class
				ON routing_observations(task_class, provider, provider_version, model);
			CREATE TABLE IF NOT EXISTS failure_fingerprints (
				fingerprint_id TEXT PRIMARY KEY,
				signature TEXT NOT NULL,
				task_class TEXT NOT NULL,
				scope TEXT NOT NULL CHECK(scope IN ('PROJECT','GENERAL')),
				project_id TEXT NOT NULL DEFAULT '',
				occurrences INTEGER NOT NULL CHECK(occurrences >= 1),
				fingerprint_json TEXT NOT NULL,
				first_seen TEXT NOT NULL,
				last_seen TEXT NOT NULL,
				expires_at TEXT
			);
			CREATE INDEX IF NOT EXISTS idx_failure_fingerprints_signature
				ON failure_fingerprints(signature, task_class);
			CREATE TABLE IF NOT EXISTS playbook_candidates (
				playbook_id TEXT PRIMARY KEY,
				memory_commit_id TEXT NOT NULL,
				title TEXT NOT NULL,
				scope TEXT NOT NULL CHECK(scope IN ('PROJECT','GENERAL')),
				project_id TEXT NOT NULL DEFAULT '',
				active INTEGER NOT NULL DEFAULT 0 CHECK(active = 0),
				candidate_json TEXT NOT NULL,
				created_at TEXT NOT NULL,
				FOREIGN KEY(memory_commit_id) REFERENCES memory_commits(memory_commit_id)
			);
			CREATE TABLE IF NOT EXISTS replay_index (
				replay_id TEXT PRIMARY KEY,
				memory_commit_id TEXT NOT NULL,
				run_id TEXT NOT NULL,
				verification_id TEXT NOT NULL,
				replay_class TEXT NOT NULL CHECK(replay_class IN ('EXACT','BEST_EFFORT','NON_REPLAYABLE')),
				tree_digest TEXT NOT NULL,
				environment_digest TEXT NOT NULL,
				evidence_bundle_digest TEXT NOT NULL,
				record_json TEXT NOT NULL,
				recorded_at TEXT NOT NULL,
				FOREIGN KEY(memory_commit_id) REFERENCES memory_commits(memory_commit_id)
			);
			CREATE TABLE IF NOT EXISTS benchmark_records (
				record_id TEXT PRIMARY KEY,
				benchmark TEXT NOT NULL,
				benchmark_version TEXT NOT NULL,
				task_id TEXT NOT NULL,
				tree_digest TEXT NOT NULL,
				mode TEXT NOT NULL,
				resolved TEXT NOT NULL CHECK(resolved IN ('PASS','FAIL','BLOCKED','NOT_RUN','UNKNOWN')),
				record_json TEXT NOT NULL,
				recorded_at TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_benchmark_records_task
				ON benchmark_records(benchmark, benchmark_version, task_id);
		`); err != nil {
			return fmt.Errorf("migrate schema version 84: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(84, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 84: %w", err)
		}
		version = 84
	}
	if version < 85 {
		// Process 08 governed optimization. A cycle is append-only and bound to
		// one exact Process 07 memory commit, so an optimization can never
		// reason about knowledge that was not actually promoted. Candidates,
		// counterfactuals, experiment results, benchmark manifests and
		// promotion records are each independently digest-protected: rewriting
		// a cycle's JSON does not launder a tampered sub-record, because the
		// sub-record's own digest is checked on read.
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS optimization_cycles (
				optimization_id TEXT PRIMARY KEY,
				version INTEGER NOT NULL CHECK(version >= 1),
				project_id TEXT NOT NULL,
				memory_commit_id TEXT NOT NULL,
				memory_commit_version INTEGER NOT NULL,
				outcome TEXT NOT NULL CHECK(outcome IN ('VERIFIED_COMPLETE','PARTIAL','FAILED','BLOCKED')),
				digest TEXT NOT NULL UNIQUE,
				cycle_json TEXT NOT NULL,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_optimization_cycles_memory
				ON optimization_cycles(memory_commit_id, memory_commit_version);
			CREATE INDEX IF NOT EXISTS idx_optimization_cycles_project
				ON optimization_cycles(project_id, created_at);
			CREATE TABLE IF NOT EXISTS optimization_candidates (
				candidate_id TEXT PRIMARY KEY,
				optimization_id TEXT NOT NULL REFERENCES optimization_cycles(optimization_id),
				dimension TEXT NOT NULL CHECK(dimension IN (
					'ROUTING','CASCADE','VERIFIER','HARNESS','NATIVE_EFFORT','CONTEXT',
					'FALLBACK','RETRY','BUDGET','TOOLS','PLAYBOOK','CONCURRENCY'
				)),
				cluster_id TEXT NOT NULL DEFAULT '',
				effects_json TEXT NOT NULL,
				candidate_json TEXT NOT NULL,
				created_at TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_optimization_candidates_cycle
				ON optimization_candidates(optimization_id);
			CREATE INDEX IF NOT EXISTS idx_optimization_candidates_cluster
				ON optimization_candidates(cluster_id);
			CREATE TABLE IF NOT EXISTS optimization_baselines (
				baseline_id TEXT PRIMARY KEY,
				optimization_id TEXT NOT NULL REFERENCES optimization_cycles(optimization_id),
				marshal_sha TEXT NOT NULL,
				environment_digest TEXT NOT NULL,
				baseline_json TEXT NOT NULL,
				recorded_at TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_optimization_baselines_cycle
				ON optimization_baselines(optimization_id);
			CREATE TABLE IF NOT EXISTS counterfactuals (
				counterfactual_id TEXT PRIMARY KEY,
				optimization_id TEXT NOT NULL REFERENCES optimization_cycles(optimization_id),
				method TEXT NOT NULL CHECK(method IN ('REPLAY','BENCHMARK','SHADOW','SYNTHETIC')),
				verdict TEXT NOT NULL CHECK(verdict IN ('ALTERNATE_BETTER','FACTUAL_BETTER','EQUIVALENT','UNKNOWN')),
				cluster_id TEXT NOT NULL DEFAULT '',
				digest TEXT NOT NULL UNIQUE,
				counterfactual_json TEXT NOT NULL,
				evaluated_at TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_counterfactuals_cycle
				ON counterfactuals(optimization_id);
			CREATE INDEX IF NOT EXISTS idx_counterfactuals_cluster
				ON counterfactuals(cluster_id);
			CREATE TABLE IF NOT EXISTS experiment_results (
				result_id TEXT PRIMARY KEY,
				optimization_id TEXT NOT NULL REFERENCES optimization_cycles(optimization_id),
				candidate_id TEXT NOT NULL DEFAULT '',
				task_id TEXT NOT NULL,
				task_class TEXT NOT NULL DEFAULT '',
				outcome TEXT NOT NULL CHECK(outcome IN ('PASS','FAIL','BLOCKED','NOT_RUN','UNKNOWN')),
				quarantined INTEGER NOT NULL CHECK(quarantined IN (0,1)),
				quarantine_reason TEXT NOT NULL DEFAULT '',
			holdout INTEGER NOT NULL CHECK(holdout IN (0,1)),
			cluster_id TEXT NOT NULL DEFAULT '',
			digest TEXT NOT NULL UNIQUE,
			result_json TEXT NOT NULL,
				observed_at TEXT NOT NULL,
				-- A quarantine must always say why: an unexplained quarantine is
				-- indistinguishable from a dropped failure.
				CHECK(quarantined = 0 OR quarantine_reason <> '')
			);
			CREATE INDEX IF NOT EXISTS idx_experiment_results_cycle
				ON experiment_results(optimization_id, candidate_id);
			CREATE INDEX IF NOT EXISTS idx_experiment_results_task
				ON experiment_results(task_id, task_class);
			CREATE TABLE IF NOT EXISTS benchmark_manifests (
				manifest_id TEXT PRIMARY KEY,
				optimization_id TEXT NOT NULL DEFAULT '',
				benchmark TEXT NOT NULL CHECK(benchmark IN ('TERMINAL_BENCH','SWE_BENCH_VERIFIED','INTERNAL')),
				official INTEGER NOT NULL CHECK(official IN (0,1)),
				evaluator_version TEXT NOT NULL,
				marshal_sha TEXT NOT NULL,
				digest TEXT NOT NULL UNIQUE,
				manifest_json TEXT NOT NULL,
				started_at TEXT NOT NULL,
				finished_at TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_benchmark_manifests_cycle
				ON benchmark_manifests(optimization_id);
			CREATE INDEX IF NOT EXISTS idx_benchmark_manifests_benchmark
				ON benchmark_manifests(benchmark, evaluator_version);
			CREATE TABLE IF NOT EXISTS promotion_records (
				promotion_id TEXT PRIMARY KEY,
				optimization_id TEXT NOT NULL REFERENCES optimization_cycles(optimization_id),
				candidate_id TEXT NOT NULL,
				baseline_id TEXT NOT NULL DEFAULT '',
				decision TEXT NOT NULL CHECK(decision IN (
					'PROMOTE','PROMOTE_BOUNDED','REJECT','NEEDS_MORE_EVIDENCE','BLOCKED','STALE'
				)),
				digest TEXT NOT NULL UNIQUE,
				promotion_json TEXT NOT NULL,
				decided_at TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_promotion_records_cycle
				ON promotion_records(optimization_id, candidate_id);
			CREATE INDEX IF NOT EXISTS idx_promotion_records_decision
				ON promotion_records(decision, decided_at);
			CREATE TABLE IF NOT EXISTS canary_rollouts (
				canary_id TEXT PRIMARY KEY,
				optimization_id TEXT NOT NULL REFERENCES optimization_cycles(optimization_id),
				candidate_id TEXT NOT NULL,
				promotion_id TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL CHECK(status IN ('PENDING','RUNNING','COMPLETED','ROLLED_BACK','HALTED')),
				exposure REAL NOT NULL CHECK(exposure >= 0.0 AND exposure <= 1.0),
				rollback_reason TEXT NOT NULL DEFAULT '',
				canary_json TEXT NOT NULL,
				started_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_canary_rollouts_cycle
				ON canary_rollouts(optimization_id, candidate_id);
			CREATE INDEX IF NOT EXISTS idx_canary_rollouts_status
				ON canary_rollouts(status, updated_at);
		`); err != nil {
			return fmt.Errorf("migrate schema version 85: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(85, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 85: %w", err)
		}
		version = 85
	}
	if version < 86 {
		// A selected model is an operator preference for future governed
		// dispatches.  It must survive a TUI restart, but it must never be
		// confused with either a provider's discovered default or a model that
		// has already been recorded as execution evidence.  The CAS revision is
		// what lets the confirmation layer refuse a selection that changed while
		// an operator was reading its prompt.
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS execution_model_preferences (
				project_id TEXT NOT NULL REFERENCES projects(project_id),
				adapter TEXT NOT NULL,
				model TEXT NOT NULL,
				revision INTEGER NOT NULL CHECK(revision >= 1),
				updated_at TEXT NOT NULL,
				PRIMARY KEY(project_id, adapter)
			);
		`); err != nil {
			return fmt.Errorf("migrate schema version 86: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(86, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 86: %w", err)
		}
		version = 86
	}
	if version < 87 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS marshal_runs (run_id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(project_id), revision INTEGER NOT NULL CHECK(revision >= 1), data_json TEXT NOT NULL);
			CREATE INDEX IF NOT EXISTS marshal_runs_project ON marshal_runs(project_id);
			CREATE TABLE IF NOT EXISTS marshal_tasks (run_id TEXT NOT NULL REFERENCES marshal_runs(run_id) ON DELETE CASCADE, task_id TEXT NOT NULL, revision INTEGER NOT NULL CHECK(revision >= 1), data_json TEXT NOT NULL, PRIMARY KEY(run_id, task_id));
			CREATE TABLE IF NOT EXISTS marshal_handins (run_id TEXT NOT NULL, task_id TEXT NOT NULL, attempt INTEGER NOT NULL CHECK(attempt >= 1), revision INTEGER NOT NULL CHECK(revision >= 1), data_json TEXT NOT NULL, PRIMARY KEY(run_id, task_id, attempt), FOREIGN KEY(run_id, task_id) REFERENCES marshal_tasks(run_id, task_id) ON DELETE CASCADE);
			CREATE TABLE IF NOT EXISTS marshal_reviews (run_id TEXT NOT NULL, task_id TEXT NOT NULL, attempt INTEGER NOT NULL CHECK(attempt >= 1), revision INTEGER NOT NULL CHECK(revision >= 1), data_json TEXT NOT NULL, PRIMARY KEY(run_id, task_id, attempt), FOREIGN KEY(run_id, task_id) REFERENCES marshal_tasks(run_id, task_id) ON DELETE CASCADE);
			CREATE TABLE IF NOT EXISTS marshal_settings (project_id TEXT PRIMARY KEY REFERENCES projects(project_id), revision INTEGER NOT NULL CHECK(revision >= 1), data_json TEXT NOT NULL);
		`); err != nil {
			return fmt.Errorf("migrate schema version 87: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES(87, ?)", utcNow()); err != nil {
			return fmt.Errorf("record schema version 87: %w", err)
		}
		version = 87
	}
	if version < 88 {
		if _, err := tx.ExecContext(ctx, `
-- Command identities follow the canonical binding, which is not the legacy
-- projects metadata row. writeCommand validates goal and grant project scope.
CREATE TABLE IF NOT EXISTS command_results (
 project_id TEXT NOT NULL, actor TEXT NOT NULL,
 command_key TEXT NOT NULL, operation TEXT NOT NULL, session_id TEXT NOT NULL,
 target_id TEXT NOT NULL, expected_version INTEGER NOT NULL, digest TEXT NOT NULL,
 result_version INTEGER NOT NULL, PRIMARY KEY(project_id,actor,command_key)
);
CREATE TABLE IF NOT EXISTS command_audit (
 revision INTEGER PRIMARY KEY AUTOINCREMENT, project_id TEXT NOT NULL,
 actor TEXT NOT NULL, operation TEXT NOT NULL, target_id TEXT NOT NULL,
 result_version INTEGER NOT NULL, result TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TRIGGER IF NOT EXISTS command_results_no_update BEFORE UPDATE ON command_results BEGIN SELECT RAISE(ABORT,'immutable command result'); END;
CREATE TRIGGER IF NOT EXISTS command_results_no_delete BEFORE DELETE ON command_results BEGIN SELECT RAISE(ABORT,'immutable command result'); END;
CREATE TRIGGER IF NOT EXISTS command_audit_no_update BEFORE UPDATE ON command_audit BEGIN SELECT RAISE(ABORT,'immutable command audit'); END;
CREATE TRIGGER IF NOT EXISTS command_audit_no_delete BEFORE DELETE ON command_audit BEGIN SELECT RAISE(ABORT,'immutable command audit'); END;
`); err != nil {
			return fmt.Errorf("migrate schema version 88: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,applied_at) VALUES(88,?)", utcNow()); err != nil {
			return err
		}
	}

	if version < 89 {
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS task_controls (task_id TEXT PRIMARY KEY REFERENCES tasks(task_id), state TEXT NOT NULL DEFAULT '', attempt INTEGER NOT NULL DEFAULT 1, session_id TEXT NOT NULL, goal_id TEXT NOT NULL DEFAULT '', goal_revision INTEGER NOT NULL DEFAULT 0, plan_id TEXT NOT NULL DEFAULT '', plan_version INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS task_supervisions (task_id TEXT PRIMARY KEY REFERENCES tasks(task_id), token TEXT NOT NULL, state TEXT NOT NULL, process_id INTEGER NOT NULL, process_stamp TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS task_command_snapshots (task_id TEXT NOT NULL REFERENCES tasks(task_id), revision INTEGER NOT NULL, data_json TEXT NOT NULL, PRIMARY KEY(task_id,revision));
CREATE TRIGGER IF NOT EXISTS task_snapshots_no_update BEFORE UPDATE ON task_command_snapshots BEGIN SELECT RAISE(ABORT,'immutable task snapshot'); END;
CREATE TRIGGER IF NOT EXISTS task_snapshots_no_delete BEFORE DELETE ON task_command_snapshots BEGIN SELECT RAISE(ABORT,'immutable task snapshot'); END;
`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,applied_at) VALUES(89,?)", utcNow()); err != nil {
			return err
		}
	}
	if version < 90 {
		// The reasoning effort future runs request is part of the same CAS-bound
		// execution preference as the model it was validated against.
		var hasEffort int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('execution_model_preferences') WHERE name = 'effort'`).Scan(&hasEffort); err != nil {
			return err
		}
		if hasEffort == 0 {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE execution_model_preferences ADD COLUMN effort TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,applied_at) VALUES(90,?)", utcNow()); err != nil {
			return err
		}
	}

	if version < 91 {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS marshal_proposals(project_id TEXT NOT NULL REFERENCES projects(project_id), occurrence_id TEXT NOT NULL, line TEXT NOT NULL, request_json TEXT NOT NULL DEFAULT '', request_key TEXT NOT NULL DEFAULT '', outcome TEXT NOT NULL DEFAULT '', PRIMARY KEY(project_id,occurrence_id))`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,applied_at) VALUES(91,?)", utcNow()); err != nil {
			return err
		}
	}
	if err := migrateCanonical(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

// MigrateArchitectureReceiver is an explicit upgrade for a separately
// governed handoff. Normal runtime startup never calls it.
// MigrateArchitectureReceiver installs the architecture receiver storage. The
// receiver steps (104-107) are part of the canonical chain, so this is the
// ordinary migration; it is kept for callers that ask for the receiver
// explicitly.
func (s *Store) MigrateArchitectureReceiver(ctx context.Context) error {
	return s.Migrate(ctx)
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
		// Repository is the durable project identity. The checked-out default
		// branch and pack version are observations that legitimately change as
		// a project evolves; treating either as a foreign-project conflict
		// strands an existing MARSHAL state after a branch rename or upgrade.
		if repository != project.Repository {
			return fmt.Errorf("%w: project %s has different identity", model.ErrConflict, project.ID)
		}
		if branch != project.DefaultBranch || pack != project.PackVersion {
			if _, err := tx.ExecContext(ctx, `
				UPDATE projects
				SET default_branch = ?, pack_version = ?
				WHERE project_id = ?
			`, project.DefaultBranch, project.PackVersion, project.ID); err != nil {
				return fmt.Errorf("update project metadata: %w", err)
			}
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
