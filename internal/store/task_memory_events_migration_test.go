package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestTaskMemoryEventsMigrationFromSchema71PreservesCanonicalMemory(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{ID: "PROJECT-EVENT-UPGRADE", Repository: "/event-repo", DefaultBranch: "main", PackVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO memory_records_v2(
			memory_id, project_id, kind, lifecycle, confidence, authority,
			title, body, scope, scope_id, source_json, content_digest,
			observed_at, ingested_at, valid_from, created_at, updated_at
		) VALUES(
			'MEM-PRE-V72', 'PROJECT-EVENT-UPGRADE', 'semantic', 'durable',
			'verified', 'verified', 'preserved', 'preserved across v72',
			'project', 'PROJECT-EVENT-UPGRADE', '{}',
			'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
			'2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z',
			'2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'
		);
		DROP TRIGGER task_memory_events_after_memory_insert;
		DROP TRIGGER task_memory_events_after_memory_update;
		DROP TABLE task_memory_events;
		DROP TABLE task_memory_event_heads;
		DELETE FROM schema_migrations WHERE version >= 72;
	`); err != nil {
		t.Fatalf("prepare schema 71 fixture: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate schema 71 to 72: %v", err)
	}
	if got := queryInt(t, st.db, "SELECT max(version) FROM schema_migrations"); got != LatestSchemaVersion {
		t.Fatalf("schema version = %d, want %d", got, LatestSchemaVersion)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM memory_records_v2 WHERE memory_id='MEM-PRE-V72'"); got != 1 {
		t.Fatalf("canonical memory rows = %d, want 1", got)
	}
	for _, table := range []string{"task_memory_event_heads", "task_memory_events"} {
		if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='"+table+"'"); got != 1 {
			t.Fatalf("migrated table %s count=%d", table, got)
		}
	}
}

func TestRegisteredTaskRevocationInitializesCriticalEventCursor(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{ID: "PROJECT-REVOKE-EVENT", Repository: "/revoke-repo", DefaultBranch: "main", PackVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterAgent(ctx, model.Agent{
		ID: "agent-revoked", ProjectID: "PROJECT-REVOKE-EVENT", DisplayName: "revoked agent",
		Role: model.RoleDeveloper, Status: model.AgentRegistered,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO tasks(task_id, project_id, title, status, risk, revision, created_at, updated_at)
		VALUES('TASK-REVOKE-EVENT', 'PROJECT-REVOKE-EVENT', 'revoke', 'ready', 'R1', 0,
		       '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
	`); err != nil {
		t.Fatal(err)
	}
	boundAt := time.Now().UTC().Add(-time.Minute)
	if err := st.PutRoleBinding(ctx, authz.RoleBinding{
		ID: "BIND-EVENT", PrincipalID: "agent-revoked", Role: "task-member",
		ScopeID: "TASK-REVOKE-EVENT", BoundBy: "operator", BoundAt: boundAt,
		PolicyDigest: "sha256:" + strings.Repeat("a", 64),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeRoleBinding(ctx, "BIND-EVENT", boundAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	events, next, more, err := st.ListTaskMemoryEvents(ctx, "PROJECT-REVOKE-EVENT", "TASK-REVOKE-EVENT", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventType != "GRANT_REVOKED" || events[0].Priority != "CRITICAL" || events[0].MemoryID != "" || next != 1 || more {
		t.Fatalf("revocation events=%+v next=%d more=%v", events, next, more)
	}
}

func TestTaskMemoryEventHistoryIsBoundedAndOldCursorFailsClosed(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{ID: "PROJECT-EVENT-BOUND", Repository: "/event-bound", DefaultBranch: "main", PackVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO task_memory_event_heads(task_id, project_id, latest_seq)
		VALUES('TASK-EVENT-BOUND', 'PROJECT-EVENT-BOUND', 0)
	`); err != nil {
		t.Fatal(err)
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxTaskMemoryEventHistory+3; i++ {
		if err := appendTaskMemoryEventTx(ctx, tx, "TASK-EVENT-BOUND", "GRANT_REVOKED", "CRITICAL", "", time.Now().UTC()); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM task_memory_events WHERE task_id='TASK-EVENT-BOUND'"); got != MaxTaskMemoryEventHistory {
		t.Fatalf("retained events=%d, want %d", got, MaxTaskMemoryEventHistory)
	}
	if _, _, _, err := st.ListTaskMemoryEvents(ctx, "PROJECT-EVENT-BOUND", "TASK-EVENT-BOUND", 1, 10); !errors.Is(err, ErrTaskMemoryCursorExpired) {
		t.Fatalf("old cursor error=%v", err)
	}
}
