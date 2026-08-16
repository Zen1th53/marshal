package gate

import (
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/policy"
)

func TestDecisionContractValidatesGatePointChecksAndDigest(t *testing.T) {
	decision := Decision{
		DecisionID:   "decision-a01",
		Point:        GatePointPrePush,
		Allowed:      true,
		Checks:       []CheckResult{{CheckID: "secret-scan", Status: CheckStatusPass, EvidenceID: "evidence-a01", Reason: CodeAllowed}},
		PolicyIDs:    []string{"policy-a01"},
		PolicyDigest: policy.PolicyDigest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		ChangeDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		CreatedAt:    time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
	}
	if err := decision.Validate(); err != nil {
		t.Fatalf("valid decision: %v", err)
	}
}

func TestDecisionContractRejectsUnknownPointAndUnknownCheckStatus(t *testing.T) {
	decision := Decision{Point: GatePoint("pre-deploy"), Checks: []CheckResult{{CheckID: "check", Status: CheckStatus("maybe")}}}
	if err := decision.Validate(); !errors.Is(err, ErrUnknownGatePoint) {
		t.Fatalf("point error=%v want=%v", err, ErrUnknownGatePoint)
	}
	decision.Point = GatePointPreExecution
	decision.DecisionID = "decision-invalid-status"
	decision.CreatedAt = time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	decision.PolicyDigest = policy.PolicyDigest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err := decision.Validate(); !errors.Is(err, ErrInvalidCheckStatus) {
		t.Fatalf("status error=%v want=%v", err, ErrInvalidCheckStatus)
	}
}
