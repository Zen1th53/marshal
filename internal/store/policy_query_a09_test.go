package store

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/policy"
)

func TestA09PolicyHotQueriesUseCanonicalIndexes(t *testing.T) {
	st, err := OpenWithObservability(context.Background(), t.TempDir()+"/policy.db", evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	record := benchmarkPolicyRecord("A09-PLAN")
	record.State = policy.StateActive
	if err := st.PutPolicy(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name, query string
		want        []string
	}{
		{"policy identity", `SELECT policy_id FROM policy_versions WHERE policy_id = ? AND version = ?`, []string{"sqlite_autoindex_policy_versions_1"}},
		{"active policy", `SELECT policy_id, version FROM policy_versions WHERE state = ?`, []string{"policy_versions_one_active"}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			args := []any{"A09-PLAN", 1}
			if check.name == "active policy" {
				args = []any{string(policy.StateActive)}
			}
			rows, err := st.db.Query(`EXPLAIN QUERY PLAN `+check.query, args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var details []string
			for rows.Next() {
				var id, parent, notused int
				var detail string
				if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			joined := strings.Join(details, "\n")
			for _, want := range check.want {
				if strings.Contains(joined, want) {
					return
				}
			}
			t.Fatalf("query plan = %v, want one of %q", details, check.want)
		})
	}
}
