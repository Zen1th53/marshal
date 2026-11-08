package tui

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestCountNewProposalsSkipsApprovedOrRejected(t *testing.T) {
	lifecycle := func(id string) (model.MemoryLifecycle, bool) {
		switch id {
		case "MEM-IMPORT-durable":
			return model.MemoryDurable, true
		case "MEM-IMPORT-rejected":
			return model.MemoryRejected, true
		case "MEM-IMPORT-candidate":
			return model.MemoryCandidate, true
		default:
			return "", false // not yet stored
		}
	}
	records := []model.MemoryRecordV2{
		{ID: "MEM-IMPORT-durable"},
		{ID: "MEM-IMPORT-rejected"},
		{ID: "MEM-IMPORT-candidate"},
		{ID: "MEM-IMPORT-new"},
	}
	seen := map[string]struct{}{}
	if got := countNewProposals(records, lifecycle, seen); got != 2 {
		t.Fatalf("counted %d new proposals, want 2", got)
	}
}

func TestCountNewProposalsDeduplicatesRePoll(t *testing.T) {
	lifecycle := func(id string) (model.MemoryLifecycle, bool) {
		return model.MemoryCandidate, true
	}
	records := []model.MemoryRecordV2{{ID: "MEM-IMPORT-a"}, {ID: "MEM-IMPORT-a"}, {ID: "MEM-IMPORT-b"}}
	seen := map[string]struct{}{}
	if got := countNewProposals(records, lifecycle, seen); got != 2 {
		t.Fatalf("counted %d new proposals across a re-poll, want 2", got)
	}
	// A later poll re-proposes the same IDs; the tally must not move.
	if got := countNewProposals(records, lifecycle, seen); got != 0 {
		t.Fatalf("re-poll added %d proposals, want 0", got)
	}
}
