package store

import (
	"context"
	"strings"
	"testing"
)

// knownMemoryTables lists every table classified as canonical or legacy memory
// in docs/memory/current-state-audit.md (T77). If a new canonical-looking
// memory table is added to the schema without being classified there, this test
// fails to enforce the convergence policy.
var knownMemoryTables = map[string]string{
	"memory_records":          "canonical-legacy: general memory, T77",
	"persistent_agent_memory": "canonical-legacy: agent-scoped memory, T77",
	"decision_records":        "canonical-legacy: task-scoped decisions, T77",
	"failure_memory_records":  "canonical-legacy: failure patterns, T77",
	// T79: canonical v2 convergence destination
	"memory_records_v2": "canonical-v2: primary memory store, T79",
	// T84: transactional outbox for derived indexers
	"memory_outbox": "canonical-outbox: derived index mutation log, T84",
	// M11: retrieval audit trail and receipts
	"memory_retrieval_receipts": "canonical-receipts: retrieval audit trail and receipts, M11",
	// Phase 2: bounded notification cursor over canonical task memory. Event
	// rows never contain memory bodies and consumers reload memory_records_v2.
	"task_memory_event_heads": "canonical-cursor: task-local monotonic sequence",
	"task_memory_events":      "canonical-cursor: bounded task change notifications",
	// Process 07: evidence-gated learning memory. These tables are a separate
	// canonical store, not a convergence target for task memory: every row is
	// bound to an exact Process 06 attestation and promoted through the
	// Process 07 evidence gates, so merging them into memory_records_v2 would
	// erase the provenance that makes them admissible.
	"memory_commits":        "canonical-p07: append-only learning commits, Process 07",
	"memory_items":          "canonical-p07: evidence-gated claim memory, Process 07",
	"memory_item_revisions": "canonical-p07: immutable claim version history, Process 07",
	"memory_dependencies":   "canonical-p07: dependency freshness graph, Process 07",
	"memory_evidence":       "canonical-p07: claim evidence and source clusters, Process 07",
}

// derivedDecisionTables are subsystem decision logs, not canonical memory.
// They must NOT be merged into the memory store.
var derivedDecisionTables = map[string]bool{
	"gate_decisions":           true,
	"egress_decisions":         true,
	"context_budget_decisions": true,
	"model_router_decisions":   true,
	"decisions":                true,
	// Process 00: the audit log of constitutional gate verdicts. It records
	// what was decided about material actions, never what the project knows,
	// so it must stay out of the memory store.
	"constitutional_decisions": true,
}

// TestT77MemoryTableInventory verifies that every table whose name contains
// "memory" or "decision" exists in the classification map above. Adding a new
// table without classifying it causes a test failure.
func TestT77MemoryTableInventory(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	rows, err := st.db.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	defer rows.Close()

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

	for _, table := range tables {
		lc := strings.ToLower(table)
		if !strings.Contains(lc, "memory") && !strings.Contains(lc, "decision") {
			continue
		}
		// Known derived decision tables are allowed without classification.
		if derivedDecisionTables[table] {
			continue
		}
		// Must be explicitly classified.
		if _, classified := knownMemoryTables[table]; !classified {
			t.Errorf(
				"unclassified memory/decision table %q found in schema.\n"+
					"Add it to docs/memory/current-state-audit.md (T77) "+
					"and update knownMemoryTables in memory_audit_t77_test.go.",
				table,
			)
		}
	}

	// Verify all known tables actually exist in the migrated schema.
	for table, classification := range knownMemoryTables {
		var n int
		err := st.db.QueryRowContext(ctx,
			`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`,
			table,
		).Scan(&n)
		if err != nil || n != 1 {
			t.Errorf("classified table %q (%s) not found in schema: err=%v n=%d",
				table, classification, err, n)
		}
	}
}

// TestT77FreshDatabaseHasExpectedMemoryTables verifies that a freshly migrated
// database exposes exactly the set of memory-related tables classified in T77.
func TestT77FreshDatabaseHasExpectedMemoryTables(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for table := range knownMemoryTables {
		var n int
		if err := st.db.QueryRowContext(ctx,
			`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`,
			table).Scan(&n); err != nil || n != 1 {
			t.Errorf("expected memory table %q to exist in fresh DB: err=%v n=%d", table, err, n)
		}
	}
}
