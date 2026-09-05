package epistemic

import (
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

// EvidenceDiscipline enforces Discipline B: Evidence Discipline.
// It ensures that evidence matches claim type, is deterministic where required,
// binds to the current commit, provides real coverage, and rejects verification theater.
type EvidenceDiscipline struct{}

func NewEvidenceDiscipline() *EvidenceDiscipline {
	return &EvidenceDiscipline{}
}

// ValidateEvidenceSuitability checks if an evidence reference is suitable to verify a claim.
func (d *EvidenceDiscipline) ValidateEvidenceSuitability(claim model.Claim, ev model.EvidenceRef) error {
	// 1. Tool execution integrity: failed tools cannot verify
	if ev.Metadata != nil {
		if exitCode, ok := ev.Metadata["exit_code"]; ok && exitCode != "0" {
			return fmt.Errorf("%w: tool exited with code %s", ErrToolExecutionFailed, exitCode)
		}
		if status, ok := ev.Metadata["status"]; ok && (status == "failed" || status == "timeout" || status == "error") {
			return fmt.Errorf("%w: tool execution status is %s", ErrToolExecutionFailed, status)
		}
	}

	// 2. Epistemic invariant: User confirmation cannot verify technical or security facts
	isUserAssertion := ev.Tool == "user-assertion" || ev.Tool == "user-prompt" || ev.Tool == "user-chat" || ev.EvidenceType == "user-confirmation"
	isTechnicalClaim := !strings.HasPrefix(claim.Scope, "ui.") && !strings.HasPrefix(claim.Scope, "ux.") && !strings.Contains(strings.ToLower(claim.Subject), "user-preference")

	if isUserAssertion && isTechnicalClaim {
		return fmt.Errorf("%w: user assertion cannot verify technical fact %q in scope %q",
			ErrUserConfirmationCannotVerifyTechnical, claim.Subject, claim.Scope)
	}

	// 3. Commit binding: If claim has a code binding commit, evidence must match it
	if claim.Binding.CommitSHA != "" && ev.CommitSHA != "" {
		if claim.Binding.CommitSHA != ev.CommitSHA {
			return fmt.Errorf("%w: claim binds to commit %s but evidence was captured on commit %s",
				ErrEvidenceWrongCommit, claim.Binding.CommitSHA, ev.CommitSHA)
		}
	}

	// 4. Verification Theater / Tautological Oracle detection
	if ev.IsOracleDerived {
		return fmt.Errorf("%w: test oracle derived from implementation under test without independent specification",
			ErrVerificationTheaterDetected)
	}
	if ev.Metadata != nil && ev.Metadata["oracle_reproduces_bug"] == "true" {
		return fmt.Errorf("%w: test oracle reproduces the exact bug implementation under test",
			ErrVerificationTheaterDetected)
	}

	// 5. Test Coverage requirement for code claims
	// If claim binds to specific files and requires deterministic test verification,
	// the test execution must have covered those files.
	if len(claim.Binding.Files) > 0 && ev.IsDeterministic && (ev.Tool == "go-test" || ev.Tool == "test" || ev.Tool == "coverage") {
		if len(ev.CoveredFiles) > 0 {
			coveredSet := make(map[string]bool)
			for _, f := range ev.CoveredFiles {
				coveredSet[f] = true
			}
			missedFiles := make([]string, 0)
			for _, f := range claim.Binding.Files {
				if !coveredSet[f] {
					missedFiles = append(missedFiles, f)
				}
			}
			if len(missedFiles) > 0 {
				return fmt.Errorf("%w: test run did not cover bound files %v", ErrInsufficientTestCoverage, missedFiles)
			}
		}
	}

	// 6. Tool type appropriateness: e.g. linter or grep alone cannot satisfy deterministic functional verification
	if ev.IsDeterministic && ev.Tool == "grep" && claim.Criticality.IsCritical() {
		return fmt.Errorf("%w: grep alone cannot verify critical claim %q", ErrWrongToolForClaim, claim.Subject)
	}

	return nil
}

// CanVerifyClaim checks whether an evidence set satisfies the requirements to transition
// a claim into VERIFIED state according to epistemic policy.
func (d *EvidenceDiscipline) CanVerifyClaim(claim model.Claim, evidenceList []model.EvidenceRef) (bool, error) {
	if len(evidenceList) == 0 {
		return false, nil
	}

	hasDeterministic := false
	for _, ev := range evidenceList {
		if err := d.ValidateEvidenceSuitability(claim, ev); err != nil {
			return false, err
		}
		if ev.IsDeterministic {
			hasDeterministic = true
		}
	}

	// For CRITICAL_BLOCKER and CRITICAL_FEATURE, at least one deterministic evidence is required
	if claim.Criticality.IsCritical() && !hasDeterministic {
		return false, nil
	}

	return hasDeterministic, nil
}
