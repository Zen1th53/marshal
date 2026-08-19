package decision_test

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/decision"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT96DecisionCanonicalConversionRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	dec := &decision.DecisionRecord{
		ID:           "DEC-100",
		TaskID:       "TASK-100",
		AgentID:      "agent-architect",
		Title:        "Adopt SQLite WAL Mode",
		Context:      "Concurrent reader/writer contention was causing lock timeouts",
		Decision:     "Enable PRAGMA journal_mode=WAL and busy_timeout=5000",
		Consequences: "Readers do not block writers, significantly improving multi-agent throughput",
		Status:       decision.StatusAccepted,
		AuthorityID:  "admin-lead",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// Convert DecisionRecord to canonical MemoryRecordV2
	mem := dec.ToMemoryRecordV2("PROJ-T96")

	if mem.ID != "DEC-100" {
		t.Fatalf("expected ID DEC-100, got: %s", mem.ID)
	}
	if mem.Kind != model.MemoryKindDecision {
		t.Fatalf("expected kind decision, got: %s", mem.Kind)
	}
	if mem.Lifecycle != model.MemoryDurable {
		t.Fatalf("accepted decision must convert to Durable lifecycle, got: %s", mem.Lifecycle)
	}
	if mem.Authority != model.AuthorityOperator {
		t.Fatalf("operator-accepted decision must have operator authority, got: %s", mem.Authority)
	}

	// Convert back from canonical MemoryRecordV2 to DecisionRecord
	restored, err := decision.FromMemoryRecordV2(mem)
	if err != nil {
		t.Fatalf("FromMemoryRecordV2: %v", err)
	}

	if restored.ID != dec.ID || restored.Title != dec.Title || restored.Decision != dec.Decision {
		t.Fatalf("restored record mismatch: %+v != %+v", restored, dec)
	}
	if restored.Status != decision.StatusAccepted {
		t.Fatalf("expected StatusAccepted, got: %s", restored.Status)
	}
	if restored.AuthorityID != "admin-lead" {
		t.Fatalf("expected authority admin-lead, got: %s", restored.AuthorityID)
	}
}

func TestT96ProposedDecisionConvertsToCandidate(t *testing.T) {
	dec := &decision.DecisionRecord{
		ID:        "DEC-PROP-1",
		TaskID:    "TASK-101",
		AgentID:   "agent-dev",
		Title:     "Proposed cache layer",
		Decision:  "Use Redis for session caching",
		Status:    decision.StatusProposed,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	mem := dec.ToMemoryRecordV2("PROJ-T96")
	if mem.Lifecycle != model.MemoryCandidate {
		t.Fatalf("proposed decision must be Candidate lifecycle, got: %s", mem.Lifecycle)
	}
	if mem.Authority != model.AuthorityAgent {
		t.Fatalf("proposed decision must have Agent authority, got: %s", mem.Authority)
	}
}
