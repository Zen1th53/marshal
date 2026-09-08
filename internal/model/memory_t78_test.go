package model_test

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// TestT78MemoryKindEnum verifies all required memory kinds are defined and
// the zero value is invalid.
func TestT78MemoryKindEnum(t *testing.T) {
	validKinds := []model.MemoryKind{
		model.MemoryKindWorking,
		model.MemoryKindSemantic,
		model.MemoryKindEpisodic,
		model.MemoryKindDecision,
		model.MemoryKindProcedural,
		model.MemoryKindFinding,
		model.MemoryKindHandoff,
		model.MemoryKindCheckpoint,
		model.MemoryKindFailure,
	}
	for _, k := range validKinds {
		if !k.IsValid() {
			t.Errorf("kind %q should be valid", k)
		}
	}
	var zero model.MemoryKind
	if zero.IsValid() {
		t.Error("zero MemoryKind should be invalid")
	}
}

// TestT78MemoryLifecycleEnum verifies lifecycle states are well-typed.
func TestT78MemoryLifecycleEnum(t *testing.T) {
	validStates := []model.MemoryLifecycle{
		model.MemoryObserved,
		model.MemoryCandidate,
		model.MemoryVerified,
		model.MemoryDurable,
		model.MemoryRejected,
		model.MemoryStale,
		model.MemoryConflicted,
		model.MemorySuperseded,
		model.MemoryTombstoned,
	}
	for _, s := range validStates {
		if !s.IsValid() {
			t.Errorf("lifecycle state %q should be valid", s)
		}
	}
	var zero model.MemoryLifecycle
	if zero.IsValid() {
		t.Error("zero MemoryLifecycle should be invalid")
	}
}

// TestT78MemoryAuthorityEnum verifies authority classes are valid.
func TestT78MemoryAuthorityEnum(t *testing.T) {
	validAuthorities := []model.MemoryAuthority{
		model.AuthorityOperator,
		model.AuthorityPolicy,
		model.AuthorityVerified,
		model.AuthorityAgent,
	}
	for _, a := range validAuthorities {
		if !a.IsValid() {
			t.Errorf("authority %q should be valid", a)
		}
	}
	var zero model.MemoryAuthority
	if zero.IsValid() {
		t.Error("zero MemoryAuthority should be invalid")
	}
}

// TestT78MemoryRecordV2Validate verifies required field validation.
func TestT78MemoryRecordV2Validate(t *testing.T) {
	now := time.Now().UTC()

	good := model.MemoryRecordV2{
		ID:         "MEM-001",
		ProjectID:  "PROJ-1",
		Kind:       model.MemoryKindDecision,
		Lifecycle:  model.MemoryDurable,
		Authority:  model.AuthorityVerified,
		Title:      "Use SQLite for single-host canonical store",
		Body:       "Decision: canonical store is SQLite for MARSHAL 1.0",
		Scope:      "project",
		ScopeID:    "PROJ-1",
		ObservedAt: now.Add(-time.Hour),
		IngestedAt: now.Add(-time.Minute),
		ValidFrom:  now.Add(-time.Hour),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := good.Validate(); err != nil {
		t.Errorf("expected valid record, got: %v", err)
	}

	// Empty ID must be rejected.
	bad := good
	bad.ID = ""
	if err := bad.Validate(); err == nil {
		t.Error("expected error for empty ID")
	}

	// Empty Body must be rejected.
	bad = good
	bad.Body = ""
	if err := bad.Validate(); err == nil {
		t.Error("expected error for empty Body")
	}

	// Zero MemoryKind must be rejected.
	bad = good
	bad.Kind = ""
	if err := bad.Validate(); err == nil {
		t.Error("expected error for zero MemoryKind")
	}

	// Zero Lifecycle must be rejected.
	bad = good
	bad.Lifecycle = ""
	if err := bad.Validate(); err == nil {
		t.Error("expected error for zero Lifecycle")
	}

	// Durable record with Authority=AuthorityAgent and no EvidenceIDs is allowed
	// (evidence binding is governance policy, not contract gate). Test that
	// no EvidenceIDs is legal for agent-authority records.
	agentRec := good
	agentRec.Authority = model.AuthorityAgent
	agentRec.EvidenceIDs = nil
	if err := agentRec.Validate(); err != nil {
		t.Errorf("agent-authority record without evidence IDs should be valid: %v", err)
	}

	// ValidFrom after ValidTo must be rejected when both are set.
	bad = good
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	bad.ValidFrom = future
	bad.ValidTo = &past
	if err := bad.Validate(); err == nil {
		t.Error("expected error for ValidFrom > ValidTo")
	}
}

// TestT78MemoryRecordV2CanonicalDigest verifies the digest excludes mutable
// retrieval/index metadata and is stable.
func TestT78MemoryRecordV2CanonicalDigest(t *testing.T) {
	now := time.Now().UTC()
	rec := model.MemoryRecordV2{
		ID:         "MEM-D1",
		ProjectID:  "PROJ-1",
		Kind:       model.MemoryKindSemantic,
		Lifecycle:  model.MemoryVerified,
		Authority:  model.AuthorityVerified,
		Title:      "Canonical digest test",
		Body:       "body text",
		Scope:      "project",
		ScopeID:    "PROJ-1",
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		Revision:   1,
	}

	d1 := rec.CanonicalDigest()
	if d1 == "" {
		t.Fatal("expected non-empty digest")
	}

	// Changing Revision (mutable) must NOT change the digest.
	rec.Revision = 99
	d2 := rec.CanonicalDigest()
	if d1 != d2 {
		t.Errorf("digest changed after Revision mutation: %s != %s", d1, d2)
	}

	// Changing Body (canonical content) must change the digest.
	rec.Body = "different body"
	d3 := rec.CanonicalDigest()
	if d1 == d3 {
		t.Error("digest did not change after Body mutation")
	}
}
