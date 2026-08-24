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
		DELETE FROM schema_migrations WHERE version >= 70;
	`); err != nil {
		t.Fatalf("prepare schema 69 fixture: %v", err)
	}

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate schema 69 to 70: %v", err)
	}
	if got := queryInt(t, st.db, "SELECT max(version) FROM schema_migrations"); got != LatestSchemaVersion {
		t.Fatalf("schema version = %d, want %d", got, LatestSchemaVersion)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM memory_retrieval_receipts"); got != 0 {
		t.Fatalf("fresh receipt rows = %d, want 0", got)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM memory_records_v2 WHERE memory_id='MEM-PRE-V70'"); got != 1 {
		t.Fatalf("preserved canonical rows = %d, want 1", got)
	}
}

func TestMemoryReceiptMigrationFromSchema70PreservesReceipts(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{ID: "PROJECT-V70", Repository: "/repo", DefaultBranch: "main", PackVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO memory_retrieval_receipts(
			receipt_id, project_id, caller_id, query_digest, created_at
		) VALUES('RCPT-V70', 'PROJECT-V70', 'agent-v70', 'digest', '2026-01-01T00:00:00Z');
		ALTER TABLE memory_retrieval_receipts DROP COLUMN evidence_ids;
		ALTER TABLE memory_retrieval_receipts DROP COLUMN outcome_memory_id;
		ALTER TABLE memory_retrieval_receipts DROP COLUMN outcome_status;
		DELETE FROM schema_migrations WHERE version >= 71;
	`); err != nil {
		t.Fatalf("prepare schema 70 fixture: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate schema 70 to 71: %v", err)
	}
	receipt, err := st.GetRetrievalReceipt(ctx, "PROJECT-V70", "RCPT-V70")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.CallerID != "agent-v70" || receipt.OutcomeMemoryID != "" || len(receipt.EvidenceIDs) != 0 {
		t.Fatalf("unexpected migrated receipt: %+v", receipt)
	}
}
