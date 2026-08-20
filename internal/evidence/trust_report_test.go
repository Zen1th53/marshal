package evidence_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
)

func TestGenerateTrustReport(t *testing.T) {
	claims := []evidence.TrustClaim{
		{
			Subject:   "TASK-001",
			Statement: "All conformance tests passed",
			Verified:  true,
		},
	}

	attestations := []evidence.TrustAttestation{
		{
			Role:      "qa",
			Actor:     "auditor-1",
			Decision:  "APPROVED",
			Timestamp: time.Now().UTC(),
			Digest:    "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		},
	}

	policy := evidence.TrustPolicySummary{
		PolicyID:     "POL-01",
		PolicyDigest: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		GatesPassed:  true,
		Violations:   0,
	}

	report, err := evidence.GenerateTrustReport("TASK-001", "c0ffee1", "c0ffee2", claims, attestations, policy, 2, 0)
	if err != nil {
		t.Fatalf("GenerateTrustReport: %v", err)
	}

	if !report.ReleaseReady {
		t.Fatal("expected report to be release ready")
	}

	if !strings.HasPrefix(report.IntegrityDigest, "sha256:") {
		t.Fatalf("expected sha256 integrity digest, got: %s", report.IntegrityDigest)
	}

	// Negative case: Unverified claim makes release_ready false
	unverifiedClaims := []evidence.TrustClaim{
		{
			Subject:   "TASK-001",
			Statement: "Unverified claim",
			Verified:  false,
		},
	}
	negReport, err := evidence.GenerateTrustReport("TASK-001", "c0ffee1", "c0ffee2", unverifiedClaims, attestations, policy, 2, 0)
	if err != nil {
		t.Fatalf("GenerateTrustReport negative: %v", err)
	}
	if negReport.ReleaseReady {
		t.Fatal("expected release_ready to be false for unverified claim")
	}
}
