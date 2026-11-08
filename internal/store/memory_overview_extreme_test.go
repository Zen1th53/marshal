//go:build !governed

package store

import (
	"fmt"
	"testing"
)

func TestMemoryOverviewExtreme(t *testing.T) {
	st := setupTestStore(t, "proj-extreme")
	ctx := t.Context()

	// Ensure extreme tables exist
	if _, err := st.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS extreme_mcp_memory_candidates(
			project_id TEXT NOT NULL,
			occurrence_id TEXT NOT NULL,
			import_key TEXT NOT NULL,
			payload_digest TEXT NOT NULL,
			memory_id TEXT NOT NULL,
			record_json TEXT NOT NULL,
			PRIMARY KEY(project_id, occurrence_id),
			UNIQUE(project_id, memory_id)
		);
		CREATE TABLE IF NOT EXISTS extreme_mcp_proposals(
			project_id TEXT NOT NULL,
			occurrence_id TEXT NOT NULL,
			launch_id TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			submission_id TEXT NOT NULL,
			digest TEXT NOT NULL,
			payload TEXT NOT NULL,
			state TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(project_id, occurrence_id),
			UNIQUE(project_id, launch_id, submission_id)
		);
	`); err != nil {
		t.Fatalf("create extreme tables: %v", err)
	}

	for i := 1; i <= 3; i++ {
		if _, err := st.db.ExecContext(ctx, `
			INSERT INTO extreme_mcp_memory_candidates(project_id, occurrence_id, import_key, payload_digest, memory_id, record_json)
			VALUES(?, ?, ?, 'digest', ?, '{}')
		`, "proj-extreme", fmt.Sprintf("occ-%d", i), fmt.Sprintf("key-%d", i), fmt.Sprintf("mem-%d", i)); err != nil {
			t.Fatalf("insert extreme_mcp_memory_candidates: %v", err)
		}
	}

	for i := 1; i <= 4; i++ {
		if _, err := st.db.ExecContext(ctx, `
			INSERT INTO extreme_mcp_proposals(project_id, occurrence_id, launch_id, owner_id, provider, submission_id, digest, payload, state)
			VALUES(?, ?, ?, 'owner', 'prov', ?, 'digest', '{}', 'admitted')
		`, "proj-extreme", fmt.Sprintf("prop-occ-%d", i), fmt.Sprintf("launch-%d", i), fmt.Sprintf("sub-%d", i)); err != nil {
			t.Fatalf("insert extreme_mcp_proposals: %v", err)
		}
	}

	overview, err := st.MemoryOverview(ctx, "proj-extreme", MemoryOverviewLimits{Queue: 10, Recalls: 10})
	if err != nil {
		t.Fatalf("MemoryOverview failed: %v", err)
	}

	if overview.MCPTools.CandidatesCount != 3 {
		t.Errorf("expected 3 candidates, got %d", overview.MCPTools.CandidatesCount)
	}
	if overview.MCPTools.ProposalsCount != 4 {
		t.Errorf("expected 4 proposals, got %d", overview.MCPTools.ProposalsCount)
	}
}
