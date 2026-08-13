package legal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func createTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	runCmd := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test Author",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test Author",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command failed: %v\nOutput: %s", err, string(out))
		}
	}

	runCmd("git", "init")
	runCmd("git", "config", "user.name", "Test Author")
	runCmd("git", "config", "user.email", "test@example.com")

	if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte("GNU AFFERO GENERAL PUBLIC LICENSE\nVersion 3"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test Repo"), 0644); err != nil {
		t.Fatal(err)
	}

	runCmd("git", "add", ".")
	runCmd("git", "commit", "-m", "Initial commit")

	return dir
}

func TestAuditStatusRules(t *testing.T) {
	repoDir := createTestRepo(t)
	report, err := RunAudit(context.Background(), repoDir)
	if err != nil {
		t.Fatalf("RunAudit failed: %v", err)
	}

	if report.Schema != "slaves.acquisition-evidence.v1" {
		t.Errorf("expected schema 'slaves.acquisition-evidence.v1', got %q", report.Schema)
	}
	if report.Source.HeadSHA == "" {
		t.Error("expected non-empty HeadSHA")
	}
	if report.Review.OverallStatus == "" {
		t.Error("expected non-empty OverallStatus")
	}
}

func TestDraftMarkerTriggersReviewRequired(t *testing.T) {
	fe := FileEvidence{
		Path:           "legal/INDIVIDUAL-CONTRIBUTOR-ASSIGNMENT.md",
		HasDraftMarker: true,
	}
	if fe.CalculateStatus() != StatusReviewRequired {
		t.Errorf("expected StatusReviewRequired for draft marker, got %v", fe.CalculateStatus())
	}
}

func TestOwnerPlaceholderTriggersReviewRequired(t *testing.T) {
	fe := FileEvidence{
		Path:                "legal/CORPORATE-CONTRIBUTOR-ASSIGNMENT.md",
		HasOwnerPlaceholder: true,
	}
	if fe.CalculateStatus() != StatusReviewRequired {
		t.Errorf("expected StatusReviewRequired for owner placeholder, got %v", fe.CalculateStatus())
	}
}

func TestMissingRequiredFileTriggersFail(t *testing.T) {
	fe := FileEvidence{
		Path:  "LICENSE",
		Error: "file missing",
	}
	if fe.CalculateStatus() != StatusFail {
		t.Errorf("expected StatusFail for missing required file, got %v", fe.CalculateStatus())
	}
}
