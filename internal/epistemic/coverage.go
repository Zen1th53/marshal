package epistemic

import (
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

// CoverageReport represents the critical claim evaluation for a Goal.
type CoverageReport struct {
	GoalID               string        `json:"goal_id"`
	GoalRevision         int64         `json:"goal_revision"`
	TotalClaims          int           `json:"total_claims"`
	CriticalClaims       int           `json:"critical_claims"`
	VerifiedCritical     int           `json:"verified_critical"`
	MissingClaims        []string      `json:"missing_claims,omitempty"`
	UnverifiedCritical   []model.Claim `json:"unverified_critical,omitempty"`
	ContestedClaims      []model.Claim `json:"contested_claims,omitempty"`
	CanSucceed           bool          `json:"can_succeed"`
	BlockerReason        string        `json:"blocker_reason,omitempty"`
}

// CriticalClaimCoverageDiscipline enforces Discipline F: Critical-Claim Coverage.
type CriticalClaimCoverageDiscipline struct{}

func NewCriticalClaimCoverageDiscipline() *CriticalClaimCoverageDiscipline {
	return &CriticalClaimCoverageDiscipline{}
}

// EvaluateCoverage audits the claim graph against the GoalContract to ensure
// all required critical claims exist, are in VERIFIED state, and have no contradictions.
// Evidence flooding cannot satisfy missing or unverified critical claims.
func (d *CriticalClaimCoverageDiscipline) EvaluateCoverage(
	goal model.GoalContract,
	claims []model.Claim,
) (CoverageReport, error) {
	report := CoverageReport{
		GoalID:             goal.ID,
		GoalRevision:       goal.Revision,
		TotalClaims:        len(claims),
		MissingClaims:      make([]string, 0),
		UnverifiedCritical: make([]model.Claim, 0),
		ContestedClaims:    make([]model.Claim, 0),
	}

	// 1. Index claims by subject and ID
	claimsBySubject := make(map[string]model.Claim)
	for _, c := range claims {
		normSubj := strings.ToLower(strings.TrimSpace(c.Subject))
		claimsBySubject[normSubj] = c

		if c.State == model.ClaimStateContested {
			report.ContestedClaims = append(report.ContestedClaims, c)
		}

		if c.Criticality.IsCritical() {
			report.CriticalClaims++
			if c.State == model.ClaimStateVerified {
				report.VerifiedCritical++
			} else {
				report.UnverifiedCritical = append(report.UnverifiedCritical, c)
			}
		}
	}

	// 2. Check for required critical claims specified in GoalContract
	for _, reqClaim := range goal.RequiredCriticalClaims {
		normReq := strings.ToLower(strings.TrimSpace(reqClaim))
		found := false
		for subj := range claimsBySubject {
			if strings.Contains(subj, normReq) || strings.Contains(normReq, subj) {
				found = true
				break
			}
		}
		if !found {
			report.MissingClaims = append(report.MissingClaims, reqClaim)
		}
	}

	// 3. Evaluate CanSucceed conditions:
	// - No missing critical claims
	// - All existing critical claims must be VERIFIED
	// - No contested claims in the goal scope
	if len(report.MissingClaims) > 0 {
		report.CanSucceed = false
		report.BlockerReason = fmt.Sprintf("Missing %d required critical claims: %v",
			len(report.MissingClaims), report.MissingClaims)
		return report, nil
	}

	if len(report.ContestedClaims) > 0 {
		report.CanSucceed = false
		report.BlockerReason = fmt.Sprintf("Goal contains %d contested claims with unresolved contradictions",
			len(report.ContestedClaims))
		return report, nil
	}

	if len(report.UnverifiedCritical) > 0 {
		report.CanSucceed = false
		report.BlockerReason = fmt.Sprintf("%d critical claims are not VERIFIED (e.g. %s is %s)",
			len(report.UnverifiedCritical), report.UnverifiedCritical[0].Subject, report.UnverifiedCritical[0].State)
		return report, nil
	}

	// All critical claims present, verified, and uncontested
	report.CanSucceed = true
	return report, nil
}
