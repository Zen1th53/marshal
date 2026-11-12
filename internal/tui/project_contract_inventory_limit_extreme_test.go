//go:build !governed

package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// A model asked for a page size the schema caps (Codex sent a larger limit
// during architecture prep and saw a red "arguments are not valid" failure).
// The page size only shapes paging, so a larger limit reads as the largest
// page and a negative one as the default; the digest and cursor still prove
// completeness.
func TestProjectContractInventoryAcceptsAnyPageSize(t *testing.T) {
	w, lease, cfg := liveMCPFixture(t)
	now := time.Now().UTC().Add(-time.Second)
	for i := 0; i < 10; i++ {
		r := model.MemoryRecordV2{ID: fmt.Sprintf("MEM-page-%02d", i), ProjectID: w.projectID, Revision: 1, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryDurable, Authority: model.AuthorityOperator, Confidence: model.ConfidenceVerified, Title: "Accepted policy", Body: "Keep it.", Scope: "project", ScopeID: w.projectID, ACLScope: lease.owner, ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, Source: model.MemorySource{Kind: "runtime_service", Reference: "owner"}}
		r.ContentDigest = r.CanonicalDigest()
		if err := w.store.WriteMemoryV2(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	child := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	for _, tc := range []struct {
		args    map[string]any
		entries int
	}{
		{map[string]any{"limit": 50}, 8},
		{map[string]any{"limit": 100, "after": ""}, 8},
		{map[string]any{"limit": -1}, 4},
		{map[string]any{}, 4},
	} {
		var page store.ProjectContractInventoryPage
		if err := json.Unmarshal([]byte(child.call(t, "marshal_project_contract_inventory", tc.args, true)), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) != tc.entries || page.NextAfterID == "" || page.CompletenessDigest == "" {
			t.Fatalf("args %v: entries %d next %q digest %q, want %d entries and a next cursor", tc.args, len(page.Entries), page.NextAfterID, page.CompletenessDigest, tc.entries)
		}
	}
	// A cursor that cannot be one is still refused, in plain words.
	msg := child.call(t, "marshal_project_contract_inventory", map[string]any{"after": strings.Repeat("x", 300)}, false)
	if msg != "The arguments for marshal_project_contract_inventory are not valid: after must be empty or the next_after_id from the previous page." {
		t.Fatalf("bad cursor refusal: %q", msg)
	}
}
