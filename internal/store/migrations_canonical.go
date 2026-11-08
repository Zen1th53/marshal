package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Schema versions 92-96 were allocated independently by three development
// lines (operator grants/restore/recovery; private snapshots, cells and
// project context; architecture receiver and knowledge leases) and mean
// different tables in each. They stay in the ledger as history only. The
// canonical sequence starts at 97, every step is idempotent, and a step runs
// when its own version is missing from the ledger, so a database from any line
// converges by adopting what already exists.
const legacyDivergentSchemaMax = 96

// Canonical migration IDs. Extreme-only steps are registered only by the
// Marshal; IDs never collide across editions.
const (
	schemaExtremeMemorySnapshots = 100
	schemaExtremeApprovedCells   = 101
	schemaExtremeSnapshotGuards  = 102
	schemaExtremeProjectContext  = 103
)

// LatestSchemaVersion is the highest canonical migration registered in this
// build. Registrations run during package initialization, before any store
// can be opened.
var LatestSchemaVersion = CommunitySchemaVersion

var canonicalMigrations = map[int]func(context.Context, *sql.Tx) error{}

func registerCanonicalMigration(version int, apply func(context.Context, *sql.Tx) error) {
	if version <= legacyDivergentSchemaMax || apply == nil || canonicalMigrations[version] != nil {
		panic(fmt.Sprintf("invalid canonical migration registration %d", version))
	}
	canonicalMigrations[version] = apply
	if version > LatestSchemaVersion {
		LatestSchemaVersion = version
	}
}

// SchemaVersionSupported reports whether a ledger maximum can be opened by
// this build. Legacy divergent versions are always accepted: their tables are
// adopted by the idempotent canonical steps.
func SchemaVersionSupported(version int) bool {
	return version <= LatestSchemaVersion || version <= legacyDivergentSchemaMax
}

// requireAdoptableTables refuses a step when a table it creates already exists
// with a different definition, so an unledgered foreign structure is never
// adopted as authority. A table created by the same statement on an earlier
// line is adopted unchanged; SQLite stores the statement without IF NOT EXISTS.
func requireAdoptableTables(ctx context.Context, tx *sql.Tx, ddl string) error {
	const prefix = "CREATE TABLE IF NOT EXISTS "
	for _, statement := range strings.Split(ddl, ";") {
		statement = strings.TrimSpace(statement)
		if !strings.HasPrefix(statement, prefix) {
			continue
		}
		rest := statement[len(prefix):]
		name := strings.TrimSpace(rest[:strings.IndexAny(rest, " (")])
		var existing string
		err := tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&existing)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect %s: %w", name, err)
		}
		if strings.Join(strings.Fields(existing), " ") != strings.Join(strings.Fields("CREATE TABLE "+rest), " ") {
			return fmt.Errorf("existing table %s has a different structure and is not adopted", name)
		}
	}
	return nil
}

func migrateCanonical(ctx context.Context, tx *sql.Tx) error {
	applied := map[int]bool{}
	rows, err := tx.QueryContext(ctx, "SELECT version FROM schema_migrations WHERE version > ?", legacyDivergentSchemaMax)
	if err != nil {
		return fmt.Errorf("read canonical migrations: %w", err)
	}
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return err
		}
		applied[version] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	versions := make([]int, 0, len(canonicalMigrations))
	for version := range canonicalMigrations {
		versions = append(versions, version)
	}
	sort.Ints(versions)
	for _, version := range versions {
		if applied[version] {
			continue
		}
		if err := canonicalMigrations[version](ctx, tx); err != nil {
			return fmt.Errorf("migration %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)", version, utcNow()); err != nil {
			return err
		}
	}
	return nil
}
