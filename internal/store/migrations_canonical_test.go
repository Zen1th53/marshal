package store

import (
	"sort"
	"testing"
)

func registeredCanonicalVersions() []int {
	versions := make([]int, 0, len(canonicalMigrations))
	for version := range canonicalMigrations {
		versions = append(versions, version)
	}
	sort.Ints(versions)
	return versions
}

func ledgerVersions(t *testing.T, s *Store) map[int]bool {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(), "SELECT version FROM schema_migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[int]bool{}
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			t.Fatal(err)
		}
		got[version] = true
	}
	return got
}

func TestCanonicalMigrationAdoptsLegacyDivergentLedger(t *testing.T) {
	s := openTestStore(t)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	// A database from any earlier line: its own 92-96 entries, none of the
	// canonical steps recorded yet.
	if _, err := s.db.ExecContext(t.Context(), "DELETE FROM schema_migrations WHERE version > ?", CommunitySchemaVersion); err != nil {
		t.Fatal(err)
	}
	for legacy := CommunitySchemaVersion + 1; legacy <= legacyDivergentSchemaMax; legacy++ {
		if _, err := s.db.ExecContext(t.Context(), "INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)", legacy, utcNow()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("legacy divergent ledger refused: %v", err)
	}
	ledger := ledgerVersions(t, s)
	for _, version := range registeredCanonicalVersions() {
		if !ledger[version] {
			t.Fatalf("canonical migration %d not adopted", version)
		}
	}
	for legacy := CommunitySchemaVersion + 1; legacy <= legacyDivergentSchemaMax; legacy++ {
		if !ledger[legacy] {
			t.Fatalf("legacy ledger entry %d was not kept as history", legacy)
		}
	}
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("canonical migration is not idempotent: %v", err)
	}
}

func TestCanonicalMigrationAppliesMissingLowerStep(t *testing.T) {
	versions := registeredCanonicalVersions()
	if len(versions) == 0 {
		t.Skip("no canonical migrations registered in this edition")
	}
	s := openTestStore(t)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	lowest := versions[0]
	if _, err := s.db.ExecContext(t.Context(), "DELETE FROM schema_migrations WHERE version = ?", lowest); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !ledgerVersions(t, s)[lowest] {
		t.Fatalf("missing lower canonical step %d was skipped because a higher one is recorded", lowest)
	}
}

func TestCanonicalMigrationRefusesNewerSchema(t *testing.T) {
	s := openTestStore(t)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	newer := LatestSchemaVersion + 1
	if newer <= legacyDivergentSchemaMax {
		newer = legacyDivergentSchemaMax + 1
	}
	if _, err := s.db.ExecContext(t.Context(), "INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)", newer, utcNow()); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(t.Context()); err == nil {
		t.Fatalf("schema %d newer than supported %d was accepted", newer, LatestSchemaVersion)
	}
	if SchemaVersionSupported(newer) {
		t.Fatalf("SchemaVersionSupported(%d) = true", newer)
	}
	for legacy := CommunitySchemaVersion + 1; legacy <= legacyDivergentSchemaMax; legacy++ {
		if !SchemaVersionSupported(legacy) {
			t.Fatalf("legacy divergent version %d rejected", legacy)
		}
	}
}
