package projectknowledge

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestKnowledgeCompactUpgradeAndForeignCacheRefusal(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned_derived_cache", true: "foreign_root"}[foreign], func(t *testing.T) {
			root, idx := fixture(t)
			db, err := idx.connection(t.Context(), true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.ExecContext(t.Context(), `CREATE TABLE metadata(id INTEGER PRIMARY KEY,root TEXT,snapshot TEXT); CREATE TABLE files(path TEXT PRIMARY KEY,hash TEXT,metadata TEXT); CREATE TABLE entries(path TEXT,line INTEGER,name TEXT,kind TEXT,package TEXT,receiver TEXT,target TEXT,file_hash TEXT);`); err != nil {
				t.Fatal(err)
			}
			binding := root
			if foreign {
				binding = t.TempDir()
			}
			old, _ := json.Marshal(Snapshot{Version: 2, PolicyDigest: "old-owned-derived-policy"})
			if _, err = db.ExecContext(t.Context(), "INSERT INTO metadata VALUES(1,?,?)", binding, string(old)); err != nil {
				t.Fatal(err)
			}
			if _, err = db.ExecContext(t.Context(), "INSERT INTO entries VALUES(?,?,?,?,?,?,?,?)", "pkg/a.go", 999, "FORGED_OLD_DEFINITION", "function", "pkg", "", "", "old"); err != nil {
				t.Fatal(err)
			}
			db.Close()
			if _, err = idx.Query(t.Context(), Query{Mode: "symbol", Text: "Helper", Limit: 10}); foreign && !errors.Is(err, ErrBinding) || !foreign && !errors.Is(err, ErrStale) {
				t.Fatal("old cache was treated as fresh or wrong-root metadata")
			}
			snapshot, err := idx.Refresh(t.Context())
			if foreign {
				if !errors.Is(err, ErrBinding) {
					t.Fatal("foreign cache was rebuilt")
				}
				db, err = idx.connection(t.Context(), false)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				var count int
				if err = db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entries WHERE name='FORGED_OLD_DEFINITION'").Scan(&count); err != nil || count != 1 {
					t.Fatal("foreign derived rows were changed before root refusal")
				}
				return
			}
			if err != nil || snapshot.Version != Version || snapshot.Stats.Reused != 0 || snapshot.Stats.Changed != 5 {
				t.Fatal("compatible cache upgrade did not rebuild source facts")
			}
			if len(query(t, idx, "symbol", "Helper", "").Items) != 1 || len(query(t, idx, "symbol", "FORGED_OLD_DEFINITION", "").Items) != 0 {
				t.Fatal("old cache rows survived upgrade")
			}
			search := query(t, idx, "search", "pkg/a.go", "")
			file := false
			for _, entry := range search.Items {
				file = file || entry.Kind == "file" && entry.Package == "pkg" && entry.FileHash != ""
			}
			if !file {
				t.Fatal("compact file search lost shared package/hash provenance")
			}
			db, err = idx.connection(t.Context(), false)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var free int
			if err = db.QueryRowContext(t.Context(), "PRAGMA freelist_count").Scan(&free); err != nil || free != 0 {
				t.Fatal("old duplicated pages were not reclaimed")
			}
			rows, err := db.QueryContext(t.Context(), "PRAGMA table_info(entries)")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var cid, notNull, pk int
				var name, typ string
				var dflt any
				if err = rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
					t.Fatal(err)
				}
				if name == "path" || name == "file_hash" || name == "package" {
					t.Fatal("repeated file identity retained in compact entries")
				}
			}
		})
	}
}
