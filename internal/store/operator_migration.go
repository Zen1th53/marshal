package store

// Operator read grants, memory restore and session recovery, from the
// operator-controls line. Both editions carry them.
const (
	schemaOperatorReadGrants = 97
	schemaMemoryRestore      = 98
	schemaSessionRecovery    = 99
)

const operatorReadGrantsSchema = `CREATE TABLE IF NOT EXISTS operator_read_grants (
			project_identity TEXT NOT NULL,
			folder TEXT NOT NULL,
			allowed INTEGER NOT NULL CHECK(allowed IN (0,1)),
			updated_at TEXT NOT NULL,
			PRIMARY KEY(project_identity, folder)
		)`

func init() {
	registerCanonicalMigration(schemaOperatorReadGrants, ddlMigration(operatorReadGrantsSchema))
	registerCanonicalMigration(schemaMemoryRestore, migrateMemoryRestore)
	registerCanonicalMigration(schemaSessionRecovery, migrateSessionRecovery)
}
