//go:build !governed

package store

import (
	"context"
	"database/sql"
)

func init() { registerCanonicalMigration(schemaExtremeProjectContext, migrateProjectContext) }

// migrateProjectContext creates empty authority structures. It changes no
// existing memory, goal, plan, approval or run, and accepts no project contract.
func migrateProjectContext(ctx context.Context, tx *sql.Tx) error {
	if err := requireAdoptableTables(ctx, tx, projectContextDDL); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, projectContextDDL)
	return err
}

const projectContextDDL = `
CREATE TABLE IF NOT EXISTS extreme_project_contract_revisions (
 project_id TEXT NOT NULL,contract_id TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),digest TEXT NOT NULL,
 contract_json TEXT NOT NULL CHECK(json_valid(contract_json)),
 acceptance_occurrence TEXT NOT NULL,created_at TEXT NOT NULL,
 PRIMARY KEY(project_id,contract_id,revision),
 UNIQUE(project_id,contract_id,revision,digest)
);
CREATE TABLE IF NOT EXISTS extreme_project_contract_active (
 project_id TEXT PRIMARY KEY,storage_project_id TEXT NOT NULL,
 contract_id TEXT,revision INTEGER,digest TEXT,
 cas_version INTEGER NOT NULL CHECK(cas_version>=0),
 bootstrap_state TEXT NOT NULL CHECK(bootstrap_state IN ('review_pending','accepted')),
 CHECK((bootstrap_state='review_pending' AND contract_id IS NULL AND revision IS NULL AND digest IS NULL)
 OR (bootstrap_state='accepted' AND contract_id IS NOT NULL AND revision IS NOT NULL AND digest IS NOT NULL)),
 FOREIGN KEY(project_id,contract_id,revision,digest)
 REFERENCES extreme_project_contract_revisions(project_id,contract_id,revision,digest)
);
CREATE TABLE IF NOT EXISTS extreme_project_contract_required_refs (
 project_id TEXT NOT NULL,contract_id TEXT NOT NULL,contract_revision INTEGER NOT NULL,
 memory_project_id TEXT NOT NULL,memory_id TEXT NOT NULL,
 memory_revision INTEGER NOT NULL CHECK(memory_revision>=0),content_digest TEXT NOT NULL,
 class TEXT NOT NULL CHECK(class IN ('policy','constraint','critical_decision','additional_fact','context_only')),
 ref_json TEXT NOT NULL CHECK(json_valid(ref_json)),
 PRIMARY KEY(project_id,contract_id,contract_revision,memory_project_id,memory_id),
 FOREIGN KEY(project_id,contract_id,contract_revision)
 REFERENCES extreme_project_contract_revisions(project_id,contract_id,revision)
);
CREATE TABLE IF NOT EXISTS extreme_project_context_receipts (
 receipt_id TEXT PRIMARY KEY,project_id TEXT NOT NULL,
 contract_id TEXT NOT NULL,contract_revision INTEGER NOT NULL,
 stage TEXT NOT NULL,parent_receipt_id TEXT REFERENCES extreme_project_context_receipts(receipt_id),
 manifest_digest TEXT NOT NULL,packet_digest TEXT NOT NULL,
 binding_json TEXT NOT NULL CHECK(json_valid(binding_json)),
 receipt_json TEXT NOT NULL CHECK(json_valid(receipt_json)),
 outcome TEXT NOT NULL,required_count INTEGER NOT NULL CHECK(required_count>=0),
 included_count INTEGER NOT NULL CHECK(included_count>=0),rendered_bytes INTEGER NOT NULL CHECK(rendered_bytes>=0),
 created_at TEXT NOT NULL,valid_until TEXT,
 FOREIGN KEY(project_id,contract_id,contract_revision)
 REFERENCES extreme_project_contract_revisions(project_id,contract_id,revision)
);
CREATE INDEX IF NOT EXISTS extreme_project_required_by_contract ON extreme_project_contract_required_refs(project_id,contract_id,contract_revision,class);
CREATE INDEX IF NOT EXISTS extreme_project_context_by_stage ON extreme_project_context_receipts(project_id,stage);
CREATE INDEX IF NOT EXISTS extreme_project_accepted_memory_inventory ON memory_records_v2(project_id,memory_id)
 WHERE lifecycle IN ('durable','verified') AND authority IN ('operator','policy');
CREATE TRIGGER IF NOT EXISTS extreme_project_contract_revision_update BEFORE UPDATE ON extreme_project_contract_revisions BEGIN SELECT RAISE(ABORT,'project contract revision is immutable'); END;
CREATE TRIGGER IF NOT EXISTS extreme_project_contract_revision_delete BEFORE DELETE ON extreme_project_contract_revisions BEGIN SELECT RAISE(ABORT,'project contract revision is immutable'); END;
CREATE TRIGGER IF NOT EXISTS extreme_project_contract_ref_update BEFORE UPDATE ON extreme_project_contract_required_refs BEGIN SELECT RAISE(ABORT,'project contract reference is immutable'); END;
CREATE TRIGGER IF NOT EXISTS extreme_project_contract_ref_delete BEFORE DELETE ON extreme_project_contract_required_refs BEGIN SELECT RAISE(ABORT,'project contract reference is immutable'); END;
CREATE TRIGGER IF NOT EXISTS extreme_project_context_receipt_update BEFORE UPDATE ON extreme_project_context_receipts BEGIN SELECT RAISE(ABORT,'project context receipt is immutable'); END;
CREATE TRIGGER IF NOT EXISTS extreme_project_context_receipt_delete BEFORE DELETE ON extreme_project_context_receipts BEGIN SELECT RAISE(ABORT,'project context receipt is immutable'); END;
`
