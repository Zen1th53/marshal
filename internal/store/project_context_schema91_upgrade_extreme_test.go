//go:build !governed

package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"
)

func schema91ProjectContextFixture(t *testing.T) *Store {
	t.Helper()
	s := projectStore(t)
	resetProjectContext95HistoricalFixture(t, s)
	// Only empty future structures are removed from this isolated fixture.
	for _, table := range []string{"extreme_cell_command_phases", "extreme_approved_cell_history", "extreme_approved_cells", "extreme_memory_restore_history", "extreme_memory_restore_previews", "extreme_memory_snapshots"} {
		var count int
		if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("historical fixture has retained private rows: %s count=%d error=%v", table, count, err)
		}
		if _, err := s.db.ExecContext(t.Context(), "DROP TABLE "+table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(t.Context(), "DELETE FROM schema_migrations WHERE version>91"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 18; i++ {
		r := contextStoreRecord(fmt.Sprintf("MEM-schema91-%02d", i), now)
		r.Body = fmt.Sprintf("Retained complete project policy number %d", i)
		r.ContentDigest = r.CanonicalDigest()
		if err := s.WriteMemoryV2(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func canonicalSchemaTableFingerprints(t *testing.T, s *Store) map[string][32]byte {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(), "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name!='schema_migrations' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	result := map[string][32]byte{}
	for _, table := range tables {
		// Names are canonical sqlite_master metadata from this owned fixture.
		rows, err := s.db.QueryContext(t.Context(), "SELECT * FROM \""+table+"\"")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		encoded := []string{}
		for rows.Next() {
			values, args := make([]any, len(columns)), make([]any, len(columns))
			for i := range args {
				args[i] = &values[i]
			}
			if err := rows.Scan(args...); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			encoded = append(encoded, string(data))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		sort.Strings(encoded)
		data, err := json.Marshal(encoded)
		if err != nil {
			t.Fatal(err)
		}
		result[table] = sha256.Sum256(data)
	}
	return result
}

func TestProjectContextSchema91UpgradePreservesCanonicalTablesAndAllMemory(t *testing.T) {
	s := schema91ProjectContextFixture(t)
	before := canonicalSchemaTableFingerprints(t, s)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	after := canonicalSchemaTableFingerprints(t, s)
	for table, digest := range before {
		if after[table] != digest {
			t.Fatalf("migration changed existing canonical table %s", table)
		}
	}
	if len(after)-len(before) != 10 {
		t.Fatal("unexpected future-only table count")
	}
	var count, version int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM memory_records_v2").Scan(&count); err != nil || count != 18 {
		t.Fatalf("full memory retention: count=%d error=%v", count, err)
	}
	if err := s.db.QueryRowContext(t.Context(), "SELECT max(version) FROM schema_migrations").Scan(&version); err != nil || version != LatestSchemaVersion {
		t.Fatalf("actual migration ledger: version=%d error=%v", version, err)
	}
	for table := range after {
		if _, existed := before[table]; existed {
			continue
		}
		if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM \""+table+"\"").Scan(&count); err != nil || count != 0 {
			t.Fatalf("migration manufactured private authority in %s", table)
		}
	}
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, canonicalSchemaTableFingerprints(t, s)) {
		t.Fatal("repeated migration changed canonical rows")
	}
}

func TestProjectContextSchema91UpgradeRefusesUnledgeredForeignDDLAtomically(t *testing.T) {
	s := schema91ProjectContextFixture(t)
	before := canonicalSchemaTableFingerprints(t, s)
	if _, err := s.db.ExecContext(t.Context(), "CREATE TABLE extreme_project_contract_revisions(forged TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(t.Context()); err == nil {
		t.Fatal("unledgered foreign authority structure was adopted")
	}
	var version int
	if err := s.db.QueryRowContext(t.Context(), "SELECT max(version) FROM schema_migrations").Scan(&version); err != nil || version != 91 {
		t.Fatalf("failed migration advanced canonical ledger: version=%d error=%v", version, err)
	}
	after := canonicalSchemaTableFingerprints(t, s)
	for table, digest := range before {
		if after[table] != digest {
			t.Fatalf("failed migration changed existing canonical table %s", table)
		}
	}
	if len(after) != len(before)+1 {
		t.Fatal("failed transaction left future-only authority structures")
	}
}
