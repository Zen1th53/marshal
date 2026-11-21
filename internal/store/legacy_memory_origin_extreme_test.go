//go:build !governed

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

// The archive table as earlier builds created it when importing legacy memory.
const legacyMemoryArchiveDDL = `CREATE TABLE IF NOT EXISTS extreme_legacy_memory_archive(project_id TEXT NOT NULL,memory_id TEXT NOT NULL,digest TEXT NOT NULL,record_json TEXT NOT NULL,receipt_json TEXT NOT NULL,PRIMARY KEY(project_id,memory_id))`

func TestLegacyMemoryArchiveKeepsHistoricalOrigin(t *testing.T) {
	s := projectStore(t)
	if _, err := s.MCPMemoryByRecordID(t.Context(), "PROJECT-local", "MEM-legacy"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("lookup without an archive must find nothing", err)
	}
	if _, err := s.db.ExecContext(t.Context(), legacyMemoryArchiveDDL); err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(model.MemoryRecordV2{ID: "MEM-legacy", ProjectID: "PROJECT-local", Title: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := json.Marshal(legacyMemoryReceipt{Occurrence: "OCC-legacy", ImportKey: "key", PayloadDigest: "digest", State: "committed"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `INSERT INTO extreme_legacy_memory_archive VALUES(?,?,?,?,?)`, "PROJECT-local", "MEM-legacy", "d", string(record), string(receipt)); err != nil {
		t.Fatal(err)
	}
	got, err := s.MCPMemoryByRecordID(t.Context(), "PROJECT-local", "MEM-legacy")
	if err != nil || got.State != "historical_migration" || got.Occurrence != "OCC-legacy" || got.Record.ID != "MEM-legacy" {
		t.Fatal("archived legacy origin was lost", got, err)
	}
	if _, err := s.MCPMemoryByRecordID(t.Context(), "PROJECT-other", "MEM-legacy"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("legacy origin crossed project scope", err)
	}
}
