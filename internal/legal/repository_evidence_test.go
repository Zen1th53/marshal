package legal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCollectLicensingAndOwnershipEvidence(t *testing.T) {
	repoDir := createTestRepo(t)
	ctx := context.Background()

	// Write mock legal files into test repo
	files := map[string]string{
		"LICENSING.md":                                   "MARSHAL Dual-Licensing",
		"COMMERCIAL-LICENSING.md":                        "Commercial Licensing Guide",
		"THIRD_PARTY_NOTICES.md":                         "Third-Party Notices",
		"LICENSES/Apache-2.0.txt":                        "Apache License 2.0",
		"docs/legal/LICENSE-HISTORY.md":                  "License History",
		"docs/legal/CHAIN-OF-TITLE.md":                   "Chain of Title",
		"docs/legal/IP-PROVENANCE-AUDIT.md":              "IP Audit",
		"docs/legal/OWNER-AND-SUCCESSOR-MODEL.md":        "Successor Model",
		"docs/legal/CONTRIBUTOR-MODEL-DECISION.md":       "Decision Doc",
		"legal/INDIVIDUAL-CONTRIBUTOR-ASSIGNMENT.md":     "DRAFT — REQUIRES QUALIFIED SOFTWARE/IP LEGAL REVIEW BEFORE USE\n[COPYRIGHT OWNER NAME / DESIGNATED ENTITY]",
		"legal/CORPORATE-CONTRIBUTOR-ASSIGNMENT.md":      "DRAFT — REQUIRES QUALIFIED SOFTWARE/IP LEGAL REVIEW BEFORE USE\n[COPYRIGHT OWNER NAME / DESIGNATED ENTITY]",
		"legal/assignment-registry.yml":                  "version: 1",
		".github/CONTRIBUTING-IP.md":                     "IP Requirements",
		".github/workflows/contributor-rights-check.yml": "name: Check",
		"CODE_OF_CONDUCT.md":                             "Code of Conduct",
		"docs/legal/THIRD-PARTY-POLICY.md":               "Third Party Policy",
		"docs/legal/AI-CONTRIBUTION-POLICY.md":           "AI Policy",
		"distribution/PACK-MANIFEST.json":                "{}",
		"VERIFICATION.json":                              "{}",
	}

	for relPath, content := range files {
		fullPath := filepath.Join(repoDir, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	runCmd := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cmd failed: %v, out: %s", err, string(out))
		}
	}
	runCmd("git", "add", ".")
	runCmd("git", "commit", "-m", "Add legal files")

	headBytes, err := execGit(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	headSHA := string(headBytes)[:40]

	lic, err := CollectLicensingEvidence(ctx, repoDir, headSHA)
	if err != nil {
		t.Fatalf("CollectLicensingEvidence failed: %v", err)
	}
	if lic.Status != StatusPass {
		t.Errorf("expected LicensingEvidence status StatusPass, got %s", lic.Status)
	}

	own, err := CollectOwnershipEvidence(ctx, repoDir, headSHA)
	if err != nil {
		t.Fatalf("CollectOwnershipEvidence failed: %v", err)
	}
	if own.IndividualAgreementICAA.HasDraftMarker != true {
		t.Error("expected HasDraftMarker for ICAA")
	}
	if own.IndividualAgreementICAA.HasOwnerPlaceholder != true {
		t.Error("expected HasOwnerPlaceholder for ICAA")
	}
	if own.OwnerIdentityStatus != StatusReviewRequired {
		t.Errorf("expected OwnerIdentityStatus StatusReviewRequired, got %s", own.OwnerIdentityStatus)
	}
	if own.Status != StatusReviewRequired {
		t.Errorf("expected OwnershipEvidence status StatusReviewRequired due to draft/placeholders, got %s", own.Status)
	}
}
