package alignment

import (
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

type DiffInspector struct{}

func NewDiffInspector() *DiffInspector {
	return &DiffInspector{}
}

type DiffSummary struct {
	DeletedFiles      []string `json:"deleted_files"`
	ModifiedFiles     []string `json:"modified_files"`
	AddedFiles        []string `json:"added_files"`
	RemovedLinesCount int      `json:"removed_lines_count"`
	AddedLinesCount   int      `json:"added_lines_count"`
}

var validationBypassPatterns = []string{
	"t.skip(",
	"//nolint",
	"/*nolint*/",
	"eslint-disable",
	"@ts-ignore",
	"skip_validation",
	"disable_security_check",
	"--exclude-test",
}

// InspectPatch analyzes file changes and unified patch text for deletion-as-satisfaction
// and validation removal.
func (di *DiffInspector) InspectPatch(
	goal model.GoalContract,
	deletedFiles []string,
	patchContent string,
	hasExplicitApprovalEvidence bool,
) ([]Violation, error) {
	var violations []Violation

	// 1. Check Deleted Files: Deletion-as-satisfaction for test files
	for _, f := range deletedFiles {
		if isTestFile(f) {
			if !isLegitimateDeletionMandated(goal, f) || !hasExplicitApprovalEvidence {
				violations = append(violations, Violation{
					Type:             CheckDeletionAsSatisfaction,
					Severity:         "BLOCKING",
					Path:             f,
					Message:          fmt.Sprintf("deletion of test file %q flagged as deletion-as-satisfaction", f),
					RequiresApproval: true,
				})
			}
		}
	}

	// 2. Inspect unified diff patch lines
	lines := strings.Split(patchContent, "\n")
	for _, line := range lines {
		// Detect test function deletions
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			trimmed := strings.TrimSpace(line[1:])
			if strings.HasPrefix(trimmed, "func Test") || strings.HasPrefix(trimmed, "it(") ||
				strings.HasPrefix(trimmed, "test(") {
				if !isLegitimateDeletionMandated(goal, trimmed) || !hasExplicitApprovalEvidence {
					violations = append(violations, Violation{
						Type:             CheckDeletionAsSatisfaction,
						Severity:         "BLOCKING",
						Message:          fmt.Sprintf("removal of test %q without Goal mandate and approval evidence", trimmed),
						RequiresApproval: true,
					})
				}
			}
			// Detect removed security or error checks
			if strings.Contains(trimmed, "if err != nil") || strings.Contains(trimmed, "authenticate(") ||
				strings.Contains(trimmed, "authorize(") || strings.Contains(trimmed, "verify_token") {
				if !isLegitimateDeletionMandated(goal, trimmed) {
					violations = append(violations, Violation{
						Type:             CheckValidationRemoval,
						Severity:         "BLOCKING",
						Message:          fmt.Sprintf("removal of validation check %q", trimmed),
						RequiresApproval: true,
					})
				}
			}
		}

		// Detect validation bypass or suppression directives added in patch
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			lower := strings.ToLower(strings.TrimSpace(line[1:]))
			for _, p := range validationBypassPatterns {
				if strings.Contains(lower, p) {
					violations = append(violations, Violation{
						Type:             CheckValidationRemoval,
						Severity:         "BLOCKING",
						Message:          fmt.Sprintf("validation bypass directive %q added in diff", p),
						RequiresApproval: true,
					})
				}
			}
		}
	}

	return violations, nil
}

func isTestFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, "_test.go") ||
		strings.HasSuffix(lower, ".test.ts") ||
		strings.HasSuffix(lower, ".test.tsx") ||
		strings.HasSuffix(lower, ".test.js") ||
		strings.HasSuffix(lower, ".spec.ts") ||
		strings.HasSuffix(lower, ".spec.js")
}

func isLegitimateDeletionMandated(goal model.GoalContract, target string) bool {
	lowerOutcome := strings.ToLower(goal.DesiredOutcome)
	lowerTarget := strings.ToLower(target)

	// Explicit mandate keywords in Goal outcome
	if strings.Contains(lowerOutcome, "deprecate") ||
		strings.Contains(lowerOutcome, "remove obsolete test") ||
		strings.Contains(lowerOutcome, "delete deprecated") ||
		strings.Contains(lowerOutcome, "decommission") {
		return true
	}

	for _, sc := range goal.SuccessCriteria {
		lowerSC := strings.ToLower(sc)
		if (strings.Contains(lowerSC, "delete") || strings.Contains(lowerSC, "remove")) &&
			strings.Contains(lowerSC, lowerTarget) {
			return true
		}
	}

	return false
}
