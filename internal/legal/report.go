package legal

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Status string

const (
	StatusPass           Status = "PASS"
	StatusReviewRequired Status = "REVIEW_REQUIRED"
	StatusFail           Status = "FAIL"
)

type FileEvidence struct {
	Path                string `json:"path"`
	Status              Status `json:"status"`
	BlobSHA256          string `json:"blob_sha256,omitempty"`
	SizeBytes           int64  `json:"size_bytes,omitempty"`
	HasDraftMarker      bool   `json:"has_draft_marker,omitempty"`
	HasOwnerPlaceholder bool   `json:"has_owner_placeholder,omitempty"`
	Error               string `json:"error,omitempty"`
}

func (fe FileEvidence) CalculateStatus() Status {
	if fe.Error != "" {
		return StatusFail
	}
	if fe.HasDraftMarker || fe.HasOwnerPlaceholder {
		return StatusReviewRequired
	}
	if fe.Status != "" {
		return fe.Status
	}
	return StatusPass
}

type CommitInfo struct {
	SHA            string    `json:"sha"`
	ParentSHAs     []string  `json:"parent_shas"`
	AuthorName     string    `json:"author_name"`
	AuthorEmail    string    `json:"author_email"`
	AuthorTime     time.Time `json:"author_time"`
	CommitterName  string    `json:"committer_name"`
	CommitterEmail string    `json:"committer_email"`
	CommitterTime  time.Time `json:"committer_time"`
	Subject        string    `json:"subject"`
}

type AuthorSummary struct {
	Name            string    `json:"name"`
	Email           string    `json:"email"`
	CommitCount     int       `json:"commit_count"`
	FirstCommitTime time.Time `json:"first_commit_time"`
	LastCommitTime  time.Time `json:"last_commit_time"`
}

type SourceEvidence struct {
	HeadSHA          string    `json:"head_sha"`
	TreeSHA          string    `json:"tree_sha"`
	CommitTime       time.Time `json:"commit_time"`
	ParentSHAs       []string  `json:"parent_shas"`
	Branch           string    `json:"branch,omitempty"`
	IsShallow        bool      `json:"is_shallow"`
	WorkingTreeClean bool      `json:"working_tree_clean"`
	HistoryComplete  bool      `json:"history_complete"`
	GoModulePath     string    `json:"go_module_path"`
	RuntimeVersion   string    `json:"runtime_version"`
	PackVersion      string    `json:"pack_version"`
	Status           Status    `json:"status"`
}

type LicensingEvidence struct {
	CurrentLicense      FileEvidence `json:"current_license"`
	HistoricalLicense   FileEvidence `json:"historical_license"`
	DualLicensingPolicy FileEvidence `json:"dual_licensing_policy"`
	CommercialLicensing FileEvidence `json:"commercial_licensing"`
	ThirdPartyNotices   FileEvidence `json:"third_party_notices"`
	LicenseHistory      FileEvidence `json:"license_history"`
	LicenseChanges      []CommitInfo `json:"license_changes,omitempty"`
	Status              Status       `json:"status"`
}

type OwnershipEvidence struct {
	ChainOfTitle             FileEvidence `json:"chain_of_title"`
	IPProvenanceAudit        FileEvidence `json:"ip_provenance_audit"`
	OwnerAndSuccessorModel   FileEvidence `json:"owner_and_successor_model"`
	ContributorModelDecision FileEvidence `json:"contributor_model_decision"`
	IndividualAgreementICAA  FileEvidence `json:"individual_agreement_icaa"`
	CorporateAgreementCCAA   FileEvidence `json:"corporate_agreement_ccaa"`
	AssignmentRegistry       FileEvidence `json:"assignment_registry"`
	ContributingIP           FileEvidence `json:"contributing_ip"`
	ContributorRightsGate    FileEvidence `json:"contributor_rights_gate"`
	OwnerIdentityStatus      Status       `json:"owner_identity_status"`
	Status                   Status       `json:"status"`
}

type DependencyItem struct {
	Path     string         `json:"path"`
	Version  string         `json:"version"`
	Indirect bool           `json:"indirect"`
	Replace  string         `json:"replace,omitempty"`
	GoModSum string         `json:"go_mod_sum,omitempty"`
	Licenses []FileEvidence `json:"licenses,omitempty"`
	Status   Status         `json:"status"`
}

type DependencyEvidence struct {
	Dependencies []DependencyItem `json:"dependencies"`
	Status       Status           `json:"status"`
}

type ThirdPartyEvidence struct {
	CodeOfConduct    FileEvidence `json:"code_of_conduct"`
	ThirdPartyPolicy FileEvidence `json:"third_party_policy"`
	AIPolicy         FileEvidence `json:"ai_policy"`
	Status           Status       `json:"status"`
}

type IntegrityEvidence struct {
	PackManifest FileEvidence `json:"pack_manifest"`
	Verification FileEvidence `json:"verification"`
	Status       Status       `json:"status"`
}

type ReviewSummary struct {
	LegalReviewRequired bool     `json:"legal_review_required"`
	DraftAgreements     []string `json:"draft_agreements,omitempty"`
	OwnerPlaceholders   []string `json:"owner_placeholders,omitempty"`
	UnresolvedItems     []string `json:"unresolved_items,omitempty"`
	OverallStatus       Status   `json:"overall_status"`
}

type Report struct {
	Schema      string             `json:"schema"`
	Source      SourceEvidence     `json:"source"`
	Licensing   LicensingEvidence  `json:"licensing"`
	Ownership   OwnershipEvidence  `json:"ownership"`
	ThirdParty  ThirdPartyEvidence `json:"third_party"`
	Dependency  DependencyEvidence `json:"dependency"`
	Integrity   IntegrityEvidence  `json:"integrity"`
	Review      ReviewSummary      `json:"review"`
	GeneratedAt time.Time          `json:"generated_at,omitempty"`
}

func (r *Report) ToJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

func (r *Report) ToTerminal() string {
	var sb strings.Builder
	sb.WriteString("SLAVES Acquisition Due-Diligence Audit\n")
	sb.WriteString("======================================\n")
	sb.WriteString(fmt.Sprintf("Source HEAD:            %s\n", r.Source.HeadSHA))
	sb.WriteString(fmt.Sprintf("Tree SHA:               %s\n", r.Source.TreeSHA))
	sb.WriteString(fmt.Sprintf("History Complete:       %v\n", r.Source.HistoryComplete))
	sb.WriteString(fmt.Sprintf("Working Tree Clean:     %v\n", r.Source.WorkingTreeClean))
	sb.WriteString("--------------------------------------\n")
	sb.WriteString(fmt.Sprintf("Current License:        %s\n", r.Licensing.CurrentLicense.Status))
	sb.WriteString(fmt.Sprintf("Historical License:     %s\n", r.Licensing.HistoricalLicense.Status))
	sb.WriteString(fmt.Sprintf("Dual-Licensing Policy:  %s\n", r.Licensing.DualLicensingPolicy.Status))
	sb.WriteString(fmt.Sprintf("Assignment Registry:    %s\n", r.Ownership.AssignmentRegistry.Status))
	sb.WriteString(fmt.Sprintf("Contributor Rights Gate:%s\n", r.Ownership.ContributorRightsGate.Status))
	sb.WriteString(fmt.Sprintf("ICAA (Individual):      %s\n", r.Ownership.IndividualAgreementICAA.Status))
	sb.WriteString(fmt.Sprintf("CCAA (Corporate):       %s\n", r.Ownership.CorporateAgreementCCAA.Status))
	sb.WriteString(fmt.Sprintf("Owner Identity:         %s\n", r.Ownership.OwnerIdentityStatus))
	sb.WriteString(fmt.Sprintf("Chain of Title:         %s\n", r.Ownership.ChainOfTitle.Status))
	sb.WriteString(fmt.Sprintf("Dependencies:           %s\n", r.Dependency.Status))
	sb.WriteString(fmt.Sprintf("Integrity Manifest:     %s\n", r.Integrity.PackManifest.Status))
	sb.WriteString("--------------------------------------\n")
	sb.WriteString(fmt.Sprintf("Overall Status:         %s\n", r.Review.OverallStatus))
	if r.Review.LegalReviewRequired {
		sb.WriteString("Notice: Legal review is required before execution of agreements.\n")
	}
	return sb.String()
}

func CombineStatuses(statuses ...Status) Status {
	hasFail := false
	hasReview := false
	for _, s := range statuses {
		if s == StatusFail {
			hasFail = true
		} else if s == StatusReviewRequired {
			hasReview = true
		}
	}
	if hasFail {
		return StatusFail
	}
	if hasReview {
		return StatusReviewRequired
	}
	return StatusPass
}
