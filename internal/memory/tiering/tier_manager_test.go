package tiering_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/tiering"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT155HotWarmColdArchivalTiering(t *testing.T) {
	ctx := context.Background()
	mgr := tiering.NewTierManager()
	now := time.Now().UTC()

	// 1. Pinned record
	pinnedRec := model.MemoryRecordV2{
		ID:        "MEM-PINNED-POL",
		Title:     "Core Project Rule",
		Body:      "No direct DB writes outside repository layer",
		Authority: model.AuthorityPolicy,
		Lifecycle: model.MemoryDurable,
	}
	mgr.RegisterRecord(pinnedRec, tiering.TierCorePinned, now)

	// 2. Unused record transitions Hot -> Warm -> Cold based on inactivity
	activeRec := model.MemoryRecordV2{
		ID:        "MEM-HOT-01",
		Title:     "Active Scratch Task",
		Body:      "Temporary build output logs",
		Lifecycle: model.MemoryCandidate,
	}
	mgr.RegisterRecord(activeRec, tiering.TierHotActive, now.Add(-60*24*time.Hour)) // 60 days old

	// Run migration sweep
	mgr.RunMigrationSweep(ctx, now)

	// Invariant 1: Pinned record is NEVER demoted
	if tier := mgr.GetRecordTier("MEM-PINNED-POL"); tier != tiering.TierCorePinned {
		t.Fatalf("expected pinned record to stay TierCorePinned, got: %s", tier)
	}

	// Invariant 2: 60-day old inactive record is demoted to Cold
	if tier := mgr.GetRecordTier("MEM-HOT-01"); tier != tiering.TierColdHistorical {
		t.Fatalf("expected 60-day old record demoted to TierColdHistorical, got: %s", tier)
	}

	// 3. Progressive disclosure retrieves cold record
	recalled, err := mgr.RecallProgressive(ctx, "MEM-HOT-01")
	if err != nil || recalled.ID != "MEM-HOT-01" {
		t.Fatalf("expected cold record retrievable via progressive recall, got: %+v (err: %v)", recalled, err)
	}
}
