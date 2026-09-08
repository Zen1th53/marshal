package learning

import (
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func gcNow() time.Time { return time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC) }

func planFor(t *testing.T, c GCCandidate) GCPlan {
	t.Helper()
	plans := PlanGC([]GCCandidate{c}, gcNow())
	if len(plans) != 1 {
		t.Fatalf("plans = %d, want 1", len(plans))
	}
	return plans[0]
}

// Raw payloads may expire, but the digest and provenance an attestation rests
// on must survive, so collection never breaks the audit chain.
func TestGCKeepsProvenanceForAttestedEvidence(t *testing.T) {
	past := gcNow().Add(-time.Hour)
	got := planFor(t, GCCandidate{
		ID: "raw-1", Class: ClassRawOutput, KeepUntil: &past,
		AttestationRefs: []string{"att-1"},
	})
	if got.Action != GCExpirePayload {
		t.Fatalf("action = %s, want EXPIRE_PAYLOAD", got.Action)
	}
}

// A canonical claim is archived rather than deleted: the audit trail outlives
// the retention window.
func TestGCArchivesCanonicalClaims(t *testing.T) {
	past := gcNow().Add(-time.Hour)
	got := planFor(t, GCCandidate{ID: "claim-1", Class: ClassCanonicalClaim, KeepUntil: &past})
	if got.Action != GCArchive {
		t.Fatalf("action = %s, want ARCHIVE", got.Action)
	}
}

// A secret is purged whatever else references it.
func TestGCPurgesSecretsRegardlessOfReferences(t *testing.T) {
	future := gcNow().Add(time.Hour)
	got := planFor(t, GCCandidate{
		ID: "sec-1", Class: ClassSecret, KeepUntil: &future,
		AttestationRefs: []string{"att-1"},
	})
	if got.Action != GCPurge {
		t.Fatalf("action = %s, want PURGE", got.Action)
	}
}

// An open retention window keeps the record.
func TestGCRetainsWithinWindow(t *testing.T) {
	future := gcNow().Add(time.Hour)
	got := planFor(t, GCCandidate{ID: "raw-1", Class: ClassRawOutput, KeepUntil: &future})
	if got.Action != GCRetain {
		t.Fatalf("action = %s, want RETAIN", got.Action)
	}
}

// The plan must be deterministic so it can be reviewed before it is applied.
func TestGCPlanIsDeterministic(t *testing.T) {
	past := gcNow().Add(-time.Hour)
	in := []GCCandidate{
		{ID: "z", Class: ClassRawOutput, KeepUntil: &past},
		{ID: "a", Class: ClassCanonicalClaim, KeepUntil: &past},
		{ID: "m", Class: ClassSecret},
	}
	first := PlanGC(in, gcNow())
	second := PlanGC(in, gcNow())
	if len(first) != 3 {
		t.Fatalf("plans = %d, want 3", len(first))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("plan is not deterministic at %d: %+v vs %+v", i, first[i], second[i])
		}
	}
	if first[0].ID != "a" || first[2].ID != "z" {
		t.Fatalf("plan is not ordered: %+v", first)
	}
}

func exportItem(id string, version int64, state model.ClaimState) Item {
	return Item{
		ID: id, Claim: "build with make", Scope: ScopeProject, ProjectID: "proj-1",
		State: state, Version: version, Provenance: "process-06",
		Evidence: []EvidenceRef{{ID: "e1", ClusterID: "c1", Digest: "d", Kind: "test"}},
	}
}

// An export omits secrets and says which records it omitted, so the backup is
// honest about being incomplete.
func TestExportRedactsSecretsAndNamesThem(t *testing.T) {
	secret := exportItem("s", 1, model.ClaimStateVerified)
	secret.Claim = "AWS_SECRET_ACCESS_KEY=AKIAIOSFODNN7EXAMPLEKEYDATA"
	b, err := Export([]Item{exportItem("a", 1, model.ClaimStateVerified), secret}, 84, "proj-1", gcNow())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(b.Items) != 1 || b.Items[0].ID != "a" {
		t.Fatalf("items = %+v, want only the safe item", b.Items)
	}
	if len(b.Redacted) != 1 || b.Redacted[0] != "s" {
		t.Fatalf("redacted = %+v, want the secret named", b.Redacted)
	}
}

// A tampered export must not restore.
func TestTamperedExportIsRejected(t *testing.T) {
	b, err := Export([]Item{exportItem("a", 1, model.ClaimStateVerified)}, 84, "proj-1", gcNow())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := VerifyExport(b); err != nil {
		t.Fatalf("VerifyExport: %v", err)
	}
	b.Items[0].Claim = "build with bazel"
	if err := VerifyExport(b); !errors.Is(err, ErrTampered) {
		t.Fatalf("VerifyExport err = %v, want ErrTampered", err)
	}
	if _, err := Restore(b, nil, 84); !errors.Is(err, ErrTampered) {
		t.Fatalf("Restore err = %v, want ErrTampered", err)
	}
}

// A restore never overwrites newer canonical memory, and never resurrects
// knowledge the store has since invalidated.
func TestRestoreDoesNotOverwriteNewerOrInvalidatedMemory(t *testing.T) {
	backup, err := Export([]Item{
		exportItem("newer-in-store", 1, model.ClaimStateVerified),
		exportItem("invalidated-in-store", 5, model.ClaimStateVerified),
		exportItem("absent-in-store", 1, model.ClaimStateVerified),
	}, 84, "proj-1", gcNow())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	current := []Item{
		exportItem("newer-in-store", 4, model.ClaimStateVerified),
		exportItem("invalidated-in-store", 1, model.ClaimStateInvalidated),
	}
	plans, err := Restore(backup, current, 84)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got := map[string]bool{}
	for _, p := range plans {
		got[p.ItemID] = p.Apply
	}
	if got["newer-in-store"] {
		t.Fatal("restore overwrote newer canonical memory")
	}
	if got["invalidated-in-store"] {
		t.Fatal("restore resurrected invalidated memory")
	}
	if !got["absent-in-store"] {
		t.Fatal("restore did not fill a genuine gap")
	}
}

// A backup from a different schema cannot be restored blind.
func TestRestoreRejectsSchemaMismatch(t *testing.T) {
	b, err := Export([]Item{exportItem("a", 1, model.ClaimStateVerified)}, 83, "proj-1", gcNow())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if _, err := Restore(b, nil, 84); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Restore err = %v, want ErrInvalid", err)
	}
}

// Retention must be explicit: a decision without a reason is not a decision.
func TestRetentionNeedsExplicitReason(t *testing.T) {
	if err := ValidateRetention(RetentionDecision{Class: "RAW_TOOL_OUTPUT"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ValidateRetention err = %v, want ErrInvalid", err)
	}
	if err := ValidateRetention(RetentionDecision{Class: "RAW_TOOL_OUTPUT", Reason: "bounded debug window"}); err != nil {
		t.Fatalf("ValidateRetention: %v", err)
	}
}
