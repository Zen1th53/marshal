//go:build !governed

package store

// Existing private stores from before the guards (legacy v92/v93) need a real ledger step: editing the original
// snapshot migration alone cannot install new preview immutability guards.
// The idempotent DDL preserves every archived body, preview and history row.
func init() { registerCanonicalMigration(schemaExtremeSnapshotGuards, migrateMemorySnapshots) }
