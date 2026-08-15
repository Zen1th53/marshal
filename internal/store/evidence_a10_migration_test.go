package store

import (
	"context"
	"testing"
)

func TestA10MigrationFromSchemaV2PreservesCanonicalRows(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if _, err := st.db.ExecContext(ctx, schemaV1); err != nil {
		t.Fatalf("create v1 schema: %v", err)
	}
	for _, stmt := range []string{
		"ALTER TABLE worker_runs ADD COLUMN runtime_instance_id TEXT;",
		"ALTER TABLE worker_runs ADD COLUMN process_start_identity TEXT;",
		"ALTER TABLE worker_runs ADD COLUMN cancellation_requested_at TEXT;",
		"ALTER TABLE worker_runs ADD COLUMN recovery_epoch INTEGER DEFAULT 0;",
	} {
		if _, err := st.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("create v2 column: %v", err)
		}
	}
	if _, err := st.db.ExecContext(ctx, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(2, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO projects(project_id, repository, default_branch, pack_version, created_at) VALUES('P1','/repo','main','1','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate v2: %v", err)
	}
	if got := queryInt(t, st.db, "SELECT max(version) FROM schema_migrations"); got != 6 {
		t.Fatalf("schema version = %d, want 6", got)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM projects WHERE project_id='P1'"); got != 1 {
		t.Fatalf("preserved projects = %d, want 1", got)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM pragma_table_info('worker_runs') WHERE name='recovery_epoch'"); got != 1 {
		t.Fatalf("v2 worker_runs column count = %d, want 1", got)
	}
}

func TestA10MigrationFromSchemaV3PreservesEvidenceAndAddsState(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if _, err := st.db.ExecContext(ctx, schemaV1); err != nil {
		t.Fatalf("create v1 schema: %v", err)
	}
	for _, stmt := range []string{
		"ALTER TABLE worker_runs ADD COLUMN runtime_instance_id TEXT;",
		"ALTER TABLE worker_runs ADD COLUMN process_start_identity TEXT;",
		"ALTER TABLE worker_runs ADD COLUMN cancellation_requested_at TEXT;",
		"ALTER TABLE worker_runs ADD COLUMN recovery_epoch INTEGER DEFAULT 0;",
	} {
		if _, err := st.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("create v2 column: %v", err)
		}
	}
	if _, err := st.db.ExecContext(ctx, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(3, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `
		CREATE TABLE evidence_nodes (
			node_id TEXT PRIMARY KEY,
			node_type TEXT NOT NULL CHECK(node_type IN ('claim','command','output','artifact','environment','verification','policy-decision')),
			digest TEXT NOT NULL UNIQUE, metadata_json TEXT NOT NULL, created_at TEXT NOT NULL
		);
		CREATE INDEX evidence_nodes_by_type ON evidence_nodes(node_type);
		CREATE INDEX evidence_nodes_by_digest ON evidence_nodes(digest);
		CREATE TABLE evidence_edges (
			from_node_id TEXT NOT NULL REFERENCES evidence_nodes(node_id),
			to_node_id TEXT NOT NULL REFERENCES evidence_nodes(node_id),
			relation TEXT NOT NULL, created_at TEXT NOT NULL,
			PRIMARY KEY(from_node_id, to_node_id, relation), CHECK(from_node_id <> to_node_id)
		);
		CREATE INDEX evidence_edges_by_to ON evidence_edges(to_node_id);
	`); err != nil {
		t.Fatalf("create v3 evidence schema: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO evidence_nodes(node_id,node_type,digest,metadata_json,created_at) VALUES('N1','claim','sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','{"source":"legacy"}','2026-01-01T00:00:00Z'),('N2','claim','sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','{"source":"target"}','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO evidence_edges(from_node_id,to_node_id,relation,created_at) VALUES('N1','N2','derived-from','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate v3: %v", err)
	}
	var state string
	if err := st.db.QueryRowContext(ctx, "SELECT state FROM evidence_nodes WHERE node_id='N1'").Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "stored" {
		t.Fatalf("migrated state = %q, want stored", state)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM evidence_edges WHERE from_node_id='N1' AND to_node_id='N2'"); got != 1 {
		t.Fatalf("preserved edges = %d, want 1", got)
	}
	var integrity string
	if err := st.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Fatalf("integrity_check = %q", integrity)
	}
}

func TestA10MigrationRejectsNewerSchemaWithoutMutation(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if _, err := st.db.ExecContext(ctx, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL); INSERT INTO schema_migrations(version, applied_at) VALUES(7, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err == nil {
		t.Fatal("newer schema was accepted")
	}
	var version int
	if err := st.db.QueryRowContext(ctx, "SELECT max(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 7 {
		t.Fatalf("schema version changed to %d", version)
	}
}
