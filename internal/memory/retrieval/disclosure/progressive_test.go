package disclosure_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/retrieval/disclosure"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT113ProgressiveDisclosureLevelsAndCeilings(t *testing.T) {
	engine := disclosure.NewEngine(disclosure.Config{
		Level1ByteCap: 200,
		Level2ByteCap: 2000,
		Level3ByteCap: 10000,
	})
	ctx := context.Background()
	now := time.Now().UTC()

	rec := model.MemoryRecordV2{
		ID:          "MEM-DISC-01",
		ProjectID:   "PROJ-1",
		Kind:        model.MemoryKindDecision,
		Lifecycle:   model.MemoryDurable,
		Authority:   model.AuthorityOperator,
		Title:       "Adopt SQLite WAL",
		Body:        "Enable PRAGMA journal_mode=WAL and busy_timeout=5000 for high concurrency operations across workers.",
		Scope:       string(model.ScopeProject),
		ScopeID:     "scope-1",
		EvidenceIDs: []string{"EVID-1", "EVID-2"},
		ObservedAt:  now,
		CreatedAt:   now,
	}

	records := map[string]model.MemoryRecordV2{
		"MEM-DISC-01": rec,
	}

	// 1. Level 1: Compact summary
	l1, err := engine.DiscloseLevel1(ctx, rec)
	if err != nil {
		t.Fatalf("DiscloseLevel1: %v", err)
	}
	if len(l1.Summary) > 200 {
		t.Fatalf("Level 1 summary exceeded byte cap: %d bytes", len(l1.Summary))
	}
	if l1.ID != "MEM-DISC-01" || l1.Title != "Adopt SQLite WAL" {
		t.Fatalf("Level 1 metadata mismatch: %+v", l1)
	}

	// 2. Level 2: Full canonical body & evidence
	l2, err := engine.DiscloseLevel2(ctx, "MEM-DISC-01", []string{"scope-1"}, func(id string) (model.MemoryRecordV2, bool) {
		r, ok := records[id]
		return r, ok
	})
	if err != nil {
		t.Fatalf("DiscloseLevel2: %v", err)
	}
	if l2.Body != rec.Body || len(l2.EvidenceIDs) != 2 {
		t.Fatalf("Level 2 payload mismatch: %+v", l2)
	}

	// 3. Level 2 expansion revoked: If record becomes tombstoned/revoked between L1 and L2
	records["MEM-DISC-01"] = model.MemoryRecordV2{
		ID:        "MEM-DISC-01",
		Lifecycle: model.MemoryTombstoned,
		ScopeID:   "scope-1",
	}
	_, err = engine.DiscloseLevel2(ctx, "MEM-DISC-01", []string{"scope-1"}, func(id string) (model.MemoryRecordV2, bool) {
		r, ok := records[id]
		return r, ok
	})
	if !errors.Is(err, disclosure.ErrRecordRevoked) {
		t.Fatalf("expected ErrRecordRevoked on tombstoned record, got: %v", err)
	}

	// 4. Level 3: Deep transcript secret scan
	transcriptWithSecret := "User: Here is key ghp_1234567890abcdefghijklmnopqrstuvwxyzAB"
	_, err = engine.DiscloseLevel3(ctx, transcriptWithSecret)
	if !errors.Is(err, security.ErrSecretDetected) {
		t.Fatalf("expected ErrSecretDetected on Level 3 transcript disclosure, got: %v", err)
	}
}
