package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type TrustClaim struct {
	Subject   string `json:"subject"`
	Statement string `json:"statement"`
	Verified  bool   `json:"verified"`
}

type TrustAttestation struct {
	Role      string    `json:"role"`
	Actor     string    `json:"actor"`
	Decision  string    `json:"decision"` // APPROVED / REJECTED
	Timestamp time.Time `json:"timestamp"`
	Digest    string    `json:"digest"`
}

type TrustPolicySummary struct {
	PolicyID     string `json:"policy_id"`
	PolicyDigest string `json:"policy_digest"`
	GatesPassed  bool   `json:"gates_passed"`
	Violations   int    `json:"violations"`
}

type TrustReport struct {
	TaskID          string             `json:"task_id"`
	BaseCommit      string             `json:"base_commit"`
	ResultCommit    string             `json:"result_commit"`
	Claims          []TrustClaim       `json:"claims"`
	Attestations    []TrustAttestation `json:"attestations"`
	PolicySummary   TrustPolicySummary `json:"policy_summary"`
	EgressAllowed   int                `json:"egress_allowed"`
	EgressDenied    int                `json:"egress_denied"`
	IntegrityDigest string             `json:"integrity_digest"`
	GeneratedAt     time.Time          `json:"generated_at"`
	ReleaseReady    bool               `json:"release_ready"`
}

func GenerateTrustReport(taskID, baseCommit, resultCommit string, claims []TrustClaim, attestations []TrustAttestation, pol TrustPolicySummary, egressAllowed, egressDenied int) (*TrustReport, error) {
	if taskID == "" || baseCommit == "" || resultCommit == "" {
		return nil, fmt.Errorf("invalid trust report parameters: missing identifiers")
	}

	report := &TrustReport{
		TaskID:        taskID,
		BaseCommit:    baseCommit,
		ResultCommit:  resultCommit,
		Claims:        claims,
		Attestations:  attestations,
		PolicySummary: pol,
		EgressAllowed: egressAllowed,
		EgressDenied:  egressDenied,
		GeneratedAt:   time.Now().UTC(),
	}

	// Determine release readiness:
	// 1. All claims must be verified.
	// 2. Policy gates must pass with 0 violations.
	// 3. Must have at least one approved attestation and zero rejected attestations.
	claimsOk := len(claims) > 0
	for _, c := range claims {
		if !c.Verified {
			claimsOk = false
			break
		}
	}

	attestationsOk := len(attestations) > 0
	for _, a := range attestations {
		if a.Decision != "APPROVED" {
			attestationsOk = false
			break
		}
	}

	policyOk := pol.GatesPassed && pol.Violations == 0

	report.ReleaseReady = claimsOk && attestationsOk && policyOk

	// Compute immutable integrity digest
	data, err := json.Marshal(struct {
		TaskID        string             `json:"task_id"`
		BaseCommit    string             `json:"base_commit"`
		ResultCommit  string             `json:"result_commit"`
		Claims        []TrustClaim       `json:"claims"`
		Attestations  []TrustAttestation `json:"attestations"`
		PolicySummary TrustPolicySummary `json:"policy_summary"`
		ReleaseReady  bool               `json:"release_ready"`
	}{
		TaskID:        report.TaskID,
		BaseCommit:    report.BaseCommit,
		ResultCommit:  report.ResultCommit,
		Claims:        report.Claims,
		Attestations:  report.Attestations,
		PolicySummary: report.PolicySummary,
		ReleaseReady:  report.ReleaseReady,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal trust report: %w", err)
	}

	h := sha256.Sum256(data)
	report.IntegrityDigest = "sha256:" + hex.EncodeToString(h[:])

	return report, nil
}
