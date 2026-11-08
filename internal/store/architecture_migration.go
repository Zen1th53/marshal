package store

import (
	"context"
	"database/sql"
)

// Architecture receiver storage and knowledge read leases, from the
// community knowledge line. Both editions carry them.
const (
	schemaArchitectureKnowledge       = 104
	schemaArchitectureOwner           = 105
	schemaArchitectureFacts           = 106
	schemaArchitectureOwnerNamespaces = 107
	schemaKnowledgeReadLeases         = 108
)

const knowledgeReadLeasesSchema = `CREATE TABLE IF NOT EXISTS knowledge_read_leases (
 lease_id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES projects(project_id),
 project_identity TEXT NOT NULL,
 task_id TEXT NOT NULL,
 principal_id TEXT NOT NULL,
 worktree TEXT NOT NULL,
 runtime_id TEXT NOT NULL,
 expires_at TEXT NOT NULL,
 revoked INTEGER NOT NULL DEFAULT 0 CHECK(revoked IN (0,1))
 )`

func init() {
	registerCanonicalMigration(schemaArchitectureKnowledge, ddlMigration(architectureKnowledgeSchema))
	registerCanonicalMigration(schemaArchitectureOwner, ddlMigration(architectureOwnerSchema))
	registerCanonicalMigration(schemaArchitectureFacts, ddlMigration(architectureFactSchema))
	registerCanonicalMigration(schemaArchitectureOwnerNamespaces, ddlMigration(architectureOwnerNamespaceSchema))
	registerCanonicalMigration(schemaKnowledgeReadLeases, ddlMigration(knowledgeReadLeasesSchema))
}

// ddlMigration runs idempotent DDL after refusing any existing table whose
// definition differs from the one the step creates.
func ddlMigration(ddl string) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		if err := requireAdoptableTables(ctx, tx, ddl); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, ddl)
		return err
	}
}
