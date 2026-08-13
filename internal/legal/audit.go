package legal

import (
	"context"
	"fmt"
)

func RunAudit(ctx context.Context, repoDir string) (*Report, error) {
	if repoDir == "" {
		return nil, fmt.Errorf("repository directory cannot be empty")
	}

	report := &Report{
		Schema: "slaves.acquisition-evidence.v1",
	}

	source, err := CollectSourceEvidence(ctx, repoDir)
	if err != nil {
		return nil, fmt.Errorf("collect source evidence: %w", err)
	}
	report.Source = *source
	report.GeneratedAt = source.CommitTime

	licensing, err := CollectLicensingEvidence(ctx, repoDir, source.HeadSHA)
	if err != nil {
		return nil, fmt.Errorf("collect licensing evidence: %w", err)
	}
	report.Licensing = *licensing

	ownership, err := CollectOwnershipEvidence(ctx, repoDir, source.HeadSHA)
	if err != nil {
		return nil, fmt.Errorf("collect ownership evidence: %w", err)
	}
	report.Ownership = *ownership

	thirdParty, err := CollectThirdPartyEvidence(ctx, repoDir, source.HeadSHA)
	if err != nil {
		return nil, fmt.Errorf("collect third-party evidence: %w", err)
	}
	report.ThirdParty = *thirdParty

	deps, err := CollectDependencyEvidence(ctx, repoDir)
	if err != nil {
		return nil, fmt.Errorf("collect dependency evidence: %w", err)
	}
	report.Dependency = *deps

	integrity, err := CollectIntegrityEvidence(ctx, repoDir, source.HeadSHA)
	if err != nil {
		return nil, fmt.Errorf("collect integrity evidence: %w", err)
	}
	report.Integrity = *integrity

	report.Review = calculateReviewSummary(report)

	return report, nil
}

func calculateReviewSummary(r *Report) ReviewSummary {
	var drafts []string
	var placeholders []string
	var unresolved []string

	checkFile := func(fe FileEvidence) {
		if fe.HasDraftMarker {
			drafts = append(drafts, fe.Path)
			unresolved = append(unresolved, fmt.Sprintf("Draft agreement: %s", fe.Path))
		}
		if fe.HasOwnerPlaceholder {
			placeholders = append(placeholders, fe.Path)
			unresolved = append(unresolved, fmt.Sprintf("Owner identity placeholder: %s", fe.Path))
		}
		if fe.Error != "" {
			unresolved = append(unresolved, fmt.Sprintf("Missing or invalid file: %s (%s)", fe.Path, fe.Error))
		}
	}

	checkFile(r.Licensing.CurrentLicense)
	checkFile(r.Licensing.HistoricalLicense)
	checkFile(r.Licensing.DualLicensingPolicy)
	checkFile(r.Licensing.CommercialLicensing)
	checkFile(r.Ownership.ChainOfTitle)
	checkFile(r.Ownership.IPProvenanceAudit)
	checkFile(r.Ownership.IndividualAgreementICAA)
	checkFile(r.Ownership.CorporateAgreementCCAA)
	checkFile(r.Ownership.AssignmentRegistry)

	overall := CombineStatuses(
		r.Source.Status,
		r.Licensing.Status,
		r.Ownership.Status,
		r.ThirdParty.Status,
		r.Dependency.Status,
		r.Integrity.Status,
	)

	legalReviewReq := len(drafts) > 0 || len(placeholders) > 0 || r.Ownership.OwnerIdentityStatus == StatusReviewRequired

	return ReviewSummary{
		LegalReviewRequired: legalReviewReq,
		DraftAgreements:     drafts,
		OwnerPlaceholders:   placeholders,
		UnresolvedItems:     unresolved,
		OverallStatus:       overall,
	}
}
