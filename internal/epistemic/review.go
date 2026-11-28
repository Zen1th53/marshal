package epistemic

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrLGTMNotEvidence          = errors.New("LGTM or casual peer agreement is not admissible evidence in MARSHAL")
	ErrVerificationTheater      = errors.New("verification theater detected: test passes unconditionally or lacks mutation sensitivity")
	ErrCriticalClaimUnverified  = errors.New("critical claim cannot be approved without deterministic verification evidence")
)

// ReviewAuditor enforces Review-the-Reviewer discipline and checks for verification theater.
type ReviewAuditor struct{}

func NewReviewAuditor() *ReviewAuditor {
	return &ReviewAuditor{}
}

// AuditClaimEvidence verifies that a reviewer's evidence meets epistemic standards.
func (a *ReviewAuditor) AuditClaimEvidence(claim model.Claim, newEvidence model.EvidenceRef) error {
	// 1. Prohibit LGTM or empty superficial endorsements
	summary := strings.TrimSpace(strings.ToUpper(newEvidence.Summary))
	if summary == "LGTM" || summary == "LOOKS GOOD TO ME" || summary == "APPROVED" || summary == "PASS" {
		return fmt.Errorf("%w: evidence summary %q contains no verifiable observation or tool output",
			ErrLGTMNotEvidence, newEvidence.Summary)
	}

	// 2. Critical claims require deterministic, non-empty evidence
	if claim.Criticality.IsCritical() {
		if !newEvidence.IsDeterministic {
			return fmt.Errorf("%w: claim %s is critical but evidence tool %s is non-deterministic",
				ErrCriticalClaimUnverified, claim.ID, newEvidence.Tool)
		}
		if newEvidence.Digest == "" {
			return fmt.Errorf("%w: claim %s is critical but evidence has no content digest",
				ErrCriticalClaimUnverified, claim.ID)
		}
	}

	// 3. Detect verification theater
	if newEvidence.IsOracleDerived {
		return fmt.Errorf("%w: evidence for claim %s was derived from the test oracle itself",
			ErrVerificationTheater, claim.ID)
	}

	return nil
}

// AuditMutationSensitivity checks whether a test suite is sensitive to intentional mutations.
// If a mutant is introduced (e.g. inverted condition or broken logic) and the test still passes,
// the test is marked as verification theater.
func (a *ReviewAuditor) AuditMutationSensitivity(testPassedWithMutant bool) error {
	if testPassedWithMutant {
		return fmt.Errorf("%w: test suite continued to pass despite intentional mutant injection; test is insensitive",
			ErrVerificationTheater)
	}
	return nil
}
