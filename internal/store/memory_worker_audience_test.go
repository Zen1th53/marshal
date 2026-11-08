package store

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestWorkerMemoryAudiencePredicatesAndMalformedMetadata(t *testing.T) {
	st := openTestStore(t)
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(t.Context(), model.Project{ID: "PROJECT-audience", Repository: "repository", DefaultBranch: "main", PackVersion: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, fixture := range []struct {
		id, kind, reference, scope string
	}{
		{"MEM-SUMMARY", "runtime_service", "source-labelled Claude summary", "project"},
		{"MEM-OPTIONAL", "", "", "project"},
		{"MEM-EXTERNAL", "external", "session", "project"},
		{"MEM-PREFIX", "agent", "external:session", "project"},
		{"MEM-IMPORT-SOURCE", "agent", "session", "project"},
		{"MEM-SHARED", "shared_channel", "session", "project"},
		{"MEM-SESSION", "agent", "session", "session"},
		{"MEM-MALFORMED", "agent", "session", "project"},
	} {
		record := model.MemoryRecordV2{ID: fixture.id, ProjectID: "PROJECT-audience", Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryDurable, Authority: model.AuthorityAgent, Title: "architecture", Body: "architecture facts", Scope: fixture.scope, ScopeID: "PROJECT-audience", Source: model.MemorySource{Kind: fixture.kind, Reference: fixture.reference}, ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now}
		if err := st.WriteMemoryV2(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate a legacy damaged metadata row, without changing its body or ACL.
	if _, err := st.db.ExecContext(t.Context(), `UPDATE memory_records_v2 SET source_json = '{bad' WHERE memory_id = 'MEM-MALFORMED'`); err != nil {
		t.Fatal(err)
	}
	// Missing optional source keys must remain eligible; SQL NULL must not
	// silently drop a valid project summary.
	if _, err := st.db.ExecContext(t.Context(), `UPDATE memory_records_v2 SET source_json = '{}' WHERE memory_id = 'MEM-OPTIONAL'`); err != nil {
		t.Fatal(err)
	}
	ids, err := st.ListAuthorizedMemoryIDsForAudience(t.Context(), "PROJECT-audience", "worker", []string{"PROJECT-audience"}, true)
	if err != nil || len(ids) != 2 {
		t.Fatalf("worker predicates: %+v %v", ids, err)
	}
	for _, expected := range []string{"MEM-SUMMARY", "MEM-OPTIONAL"} {
		if _, exists := ids[expected]; !exists {
			t.Fatalf("valid summary excluded: %s", expected)
		}
	}
	fallback, err := st.SearchAuthorizedMemoryIDsForAudience(t.Context(), "PROJECT-audience", "worker", []string{"PROJECT-audience"}, "architecture", 50, true)
	if err != nil || len(fallback) != 2 {
		t.Fatalf("cold fallback disagrees with audience: %+v %v", fallback, err)
	}
	all, err := st.ListAuthorizedMemoryIDs(t.Context(), "PROJECT-audience", "marshal", []string{"PROJECT-audience"})
	if err != nil || len(all) != 8 {
		t.Fatalf("default audience changed: %+v %v", all, err)
	}
}
