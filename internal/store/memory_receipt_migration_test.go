package store

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestMemoryReceiptMigrationFromSchema69PreservesCanonicalRows(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}
	if err := st.InitProject(ctx, model.Project{
		ID: "PROJECT-RECEIPT-UPGRADE", Repository: "/repo", DefaultBranch: "main", PackVersion: "1",
	}); err != nil {
		t.Fatalf("init project: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO memory_records_v2(
			memory_id, project_id, kind, lifecycle, confidence, authority,
			title, body, scope, scope_id, source_json, content_digest,
			observed_at, ingested_at, valid_from, created_at, updated_at
		) VALUES(
			'MEM-PRE-V70', 'PROJECT-RECEIPT-UPGRADE', 'semantic', 'durable',
			'verified', 'verified', 'preserved', 'preserved across v70',
			'project', 'PROJECT-RECEIPT-UPGRADE', '{}',
			'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
			'2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z',
			'2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'
		);
		DROP TABLE memory_retrieval_receipts;
		DELETE FROM schema_migrations WHERE version = 70;
	`); err != nil {
		t.Fatalf("prepare schema 69 fixture: %v", err)
	}

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate schema 69 to 70: %v", err)
	}
	if got := queryInt(t, st.db, "SELECT max(version) FROM schema_migrations"); got != 70 {
		t.Fatalf("schema version = %d, want 70", got)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM memory_retrieval_receipts"); got != 0 {
		t.Fatalf("fresh receipt rows = %d, want 0", got)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM memory_records_v2 WHERE memory_id='MEM-PRE-V70'"); got != 1 {
		t.Fatalf("preserved canonical rows = %d, want 1", got)
	}
}
