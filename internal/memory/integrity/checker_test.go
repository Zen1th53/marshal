package integrity_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/integrity"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT128MemoryIntegrityAndTamperDetection(t *testing.T) {
	checker := integrity.NewChecker()
	ctx := context.Background()
	now := time.Now().UTC()

	// 1. Untampered record passes cleanly
	cleanRec := model.MemoryRecordV2{
		ID:         "MEM-CLEAN-01",
		ProjectID:  "PROJ-1",
		Kind:       model.MemoryKindDecision,
		Lifecycle:  model.MemoryDurable,
		Title:      "SQLite WAL Configuration",
		Body:       "PRAGMA journal_mode=WAL;",
		ObservedAt: now,
	}
	cleanRec.ContentDigest = cleanRec.CanonicalDigest()

	reportClean, err := checker.CheckRecord(ctx, cleanRec)
	if err != nil || !reportClean.Valid {
		t.Fatalf("expected clean record to pass, got: %+v (err: %v)", reportClean, err)
	}

	// 2. Tampered record (body changed directly in DB without updating digest)
	tamperedRec := cleanRec
	tamperedRec.Body = "MALICIOUSLY TAMPERED SQL INJECTION BODY"

	reportTampered, _ := checker.CheckRecord(ctx, tamperedRec)
	if reportTampered.Valid || len(reportTampered.Violations) == 0 {
		t.Fatal("expected tamper detection on corrupted body digest")
	}
	if reportTampered.Violations[0].Kind != integrity.ViolationContentDigestMismatch {
		t.Fatalf("expected ViolationContentDigestMismatch, got: %s", reportTampered.Violations[0].Kind)
	}

	// 3. Orphan / missing evidence detection
	orphanEvidenceRec := cleanRec
	orphanEvidenceRec.EvidenceIDs = []string{"NONEXISTENT-EVIDENCE-ID-999"}
	orphanEvidenceRec.ContentDigest = orphanEvidenceRec.CanonicalDigest()

	reportOrphan, _ := checker.CheckEvidenceLineage(ctx, orphanEvidenceRec, func(id string) bool {
		return false // Evidence does not exist
	})
	if reportOrphan.Valid || len(reportOrphan.Violations) == 0 {
		t.Fatal("expected orphan evidence violation")
	}

	// 4. Index watermark check
	reportWatermark := checker.CheckIndexWatermark(100, 90) // canonical rev 100 vs index rev 90
	if reportWatermark.Valid {
		t.Fatal("expected watermark mismatch when index lags behind canonical revision")
	}
}
