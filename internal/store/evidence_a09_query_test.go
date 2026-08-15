package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
)

func TestA09NeighborsDoesNotDeadlockOnSingleSQLiteConnection(t *testing.T) {
	st := openEvidenceStore(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
	from := testEvidenceNode("A09-NEIGHBOR-FROM", "claim", "from")
	to := testEvidenceNode("A09-NEIGHBOR-TO", "claim", "to")
	if _, err := st.PutNode(context.Background(), from); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutNode(context.Background(), to); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Link(context.Background(), evidence.Edge{From: from.ID, To: to.ID, Relation: "derived-from"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	neighbors, err := st.Neighbors(ctx, from.ID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Neighbors deadlocked on single SQLite connection: %v", err)
		}
		t.Fatal(err)
	}
	if len(neighbors) != 1 || neighbors[0].ID != to.ID {
		t.Fatalf("neighbors = %#v", neighbors)
	}
}

func TestA09EvidenceQueryPlansUseCanonicalIndexes(t *testing.T) {
	st := openEvidenceStore(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
	from := testEvidenceNode("A09-PLAN-FROM", "claim", "from")
	to := testEvidenceNode("A09-PLAN-TO", "claim", "to")
	if _, err := st.PutNode(context.Background(), from); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutNode(context.Background(), to); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Link(context.Background(), evidence.Edge{From: from.ID, To: to.ID, Relation: "derived-from"}); err != nil {
		t.Fatal(err)
	}

	checks := []struct {
		name, query string
		wants       []string
	}{
		{"node id", `SELECT node_id FROM evidence_nodes WHERE node_id = ?`, []string{"sqlite_autoindex_evidence_nodes_1"}},
		{"node digest", `SELECT node_id FROM evidence_nodes WHERE digest = ?`, []string{"evidence_nodes_by_digest", "sqlite_autoindex_evidence_nodes_2"}},
		{"node type", `SELECT node_id FROM evidence_nodes WHERE node_type = ?`, []string{"evidence_nodes_by_type"}},
		{"edge from", `SELECT to_node_id FROM evidence_edges WHERE from_node_id = ?`, []string{"sqlite_autoindex_evidence_edges_1"}},
		{"edge to", `SELECT from_node_id FROM evidence_edges WHERE to_node_id = ?`, []string{"evidence_edges_by_to"}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			rows, err := st.db.Query(`EXPLAIN QUERY PLAN `+check.query, "unused")
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
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(details, "\n")
			for _, want := range check.wants {
				if strings.Contains(joined, want) {
					return
				}
			}
			t.Fatalf("query plan = %v, want one of indexes %q", details, check.wants)
		})
	}
}
