package legal

import (
	"context"
	"strings"
)

const draftMarker = "DRAFT — REQUIRES QUALIFIED SOFTWARE/IP LEGAL REVIEW BEFORE USE"
const ownerPlaceholderMarker = "[COPYRIGHT OWNER NAME / DESIGNATED ENTITY]"

func auditFile(ctx context.Context, repoDir string, headSHA string, relPath string) FileEvidence {
	data, err := ReadBlob(ctx, repoDir, headSHA, relPath)
	if err != nil {
		return FileEvidence{
			Path:   relPath,
			Status: StatusFail,
			Error:  err.Error(),
		}
	}

	content := string(data)
	hasDraft := strings.Contains(content, "DRAFT") && strings.Contains(content, "LEGAL REVIEW")
	hasOwnerPlaceholder := strings.Contains(content, ownerPlaceholderMarker) || strings.Contains(content, "[COPYRIGHT OWNER NAME]")

	fe := FileEvidence{
		Path:                relPath,
		BlobSHA256:          HashBytes(data),
		SizeBytes:           int64(len(data)),
		HasDraftMarker:      hasDraft,
		HasOwnerPlaceholder: hasOwnerPlaceholder,
	}
	fe.Status = fe.CalculateStatus()
	return fe
}

func CollectLicensingEvidence(ctx context.Context, repoDir string, headSHA string) (*LicensingEvidence, error) {
	currLic := auditFile(ctx, repoDir, headSHA, "LICENSE")
	histLic := auditFile(ctx, repoDir, headSHA, "LICENSES/Apache-2.0.txt")
	dualPol := auditFile(ctx, repoDir, headSHA, "LICENSING.md")
	commLic := auditFile(ctx, repoDir, headSHA, "COMMERCIAL-LICENSING.md")
	tpNotices := auditFile(ctx, repoDir, headSHA, "THIRD_PARTY_NOTICES.md")
	licHist := auditFile(ctx, repoDir, headSHA, "docs/legal/LICENSE-HISTORY.md")

	overall := CombineStatuses(
		currLic.Status,
		histLic.Status,
		dualPol.Status,
		commLic.Status,
		tpNotices.Status,
		licHist.Status,
	)

	return &LicensingEvidence{
		CurrentLicense:      currLic,
		HistoricalLicense:   histLic,
		DualLicensingPolicy: dualPol,
		CommercialLicensing: commLic,
		ThirdPartyNotices:   tpNotices,
		LicenseHistory:      licHist,
		Status:              overall,
	}, nil
}

func CollectOwnershipEvidence(ctx context.Context, repoDir string, headSHA string) (*OwnershipEvidence, error) {
	cot := auditFile(ctx, repoDir, headSHA, "docs/legal/CHAIN-OF-TITLE.md")
	ipAudit := auditFile(ctx, repoDir, headSHA, "docs/legal/IP-PROVENANCE-AUDIT.md")
	ownerSucc := auditFile(ctx, repoDir, headSHA, "docs/legal/OWNER-AND-SUCCESSOR-MODEL.md")
	contribModel := auditFile(ctx, repoDir, headSHA, "docs/legal/CONTRIBUTOR-MODEL-DECISION.md")
	icaa := auditFile(ctx, repoDir, headSHA, "legal/INDIVIDUAL-CONTRIBUTOR-ASSIGNMENT.md")
	ccaa := auditFile(ctx, repoDir, headSHA, "legal/CORPORATE-CONTRIBUTOR-ASSIGNMENT.md")
	registry := auditFile(ctx, repoDir, headSHA, "legal/assignment-registry.yml")
	contribIP := auditFile(ctx, repoDir, headSHA, ".github/CONTRIBUTING-IP.md")
	rightsGate := auditFile(ctx, repoDir, headSHA, ".github/workflows/contributor-rights-check.yml")

	ownerIdentStatus := StatusPass
	if icaa.HasOwnerPlaceholder || ccaa.HasOwnerPlaceholder {
		ownerIdentStatus = StatusReviewRequired
	}

	overall := CombineStatuses(
		cot.Status,
		ipAudit.Status,
		ownerSucc.Status,
		contribModel.Status,
		icaa.Status,
		ccaa.Status,
		registry.Status,
		contribIP.Status,
		rightsGate.Status,
		ownerIdentStatus,
	)

	return &OwnershipEvidence{
		ChainOfTitle:             cot,
		IPProvenanceAudit:        ipAudit,
		OwnerAndSuccessorModel:   ownerSucc,
		ContributorModelDecision: contribModel,
		IndividualAgreementICAA:  icaa,
		CorporateAgreementCCAA:   ccaa,
		AssignmentRegistry:       registry,
		ContributingIP:           contribIP,
		ContributorRightsGate:    rightsGate,
		OwnerIdentityStatus:      ownerIdentStatus,
		Status:                   overall,
	}, nil
}

func CollectThirdPartyEvidence(ctx context.Context, repoDir string, headSHA string) (*ThirdPartyEvidence, error) {
	coc := auditFile(ctx, repoDir, headSHA, "CODE_OF_CONDUCT.md")
	tpPol := auditFile(ctx, repoDir, headSHA, "docs/legal/THIRD-PARTY-POLICY.md")
	aiPol := auditFile(ctx, repoDir, headSHA, "docs/legal/AI-CONTRIBUTION-POLICY.md")

	overall := CombineStatuses(coc.Status, tpPol.Status, aiPol.Status)

	return &ThirdPartyEvidence{
		CodeOfConduct:    coc,
		ThirdPartyPolicy: tpPol,
		AIPolicy:         aiPol,
		Status:           overall,
	}, nil
}

func CollectIntegrityEvidence(ctx context.Context, repoDir string, headSHA string) (*IntegrityEvidence, error) {
	manifest := auditFile(ctx, repoDir, headSHA, "distribution/PACK-MANIFEST.json")
	verif := auditFile(ctx, repoDir, headSHA, "VERIFICATION.json")

	overall := CombineStatuses(manifest.Status, verif.Status)

	return &IntegrityEvidence{
		PackManifest: manifest,
		Verification: verif,
		Status:       overall,
	}, nil
}
