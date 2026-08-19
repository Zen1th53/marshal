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
}

// derivedDecisionTables are subsystem decision logs, not canonical memory.
// They must NOT be merged into the memory store.
var derivedDecisionTables = map[string]bool{
	"gate_decisions":           true,
	"egress_decisions":         true,
	"context_budget_decisions": true,
	"model_router_decisions":   true,
	"decisions":                true,
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
