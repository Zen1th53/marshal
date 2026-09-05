package alignment

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

type BlastRadiusAuditor struct{}

func NewBlastRadiusAuditor() *BlastRadiusAuditor {
	return &BlastRadiusAuditor{}
}

// AuditBlastRadius compares predicted files against observed changed files,
// checking both path scope boundaries and blast radius magnitude.
func (b *BlastRadiusAuditor) AuditBlastRadius(
	goal model.GoalContract,
	predictedFiles []string,
	observedFiles []string,
	maxRadiusMultiplier int,
) ([]Violation, error) {
	var violations []Violation

	predictedCount := len(predictedFiles)
	observedCount := len(observedFiles)

	if maxRadiusMultiplier <= 0 {
		maxRadiusMultiplier = 3 // default maximum 3x predicted radius for small tasks
	}

	// 1. Magnitude check: If task predicted small scope (e.g. 1 file), but touches 30 files
	if predictedCount > 0 {
		maxAllowed := predictedCount * maxRadiusMultiplier
		if maxAllowed < 3 {
			maxAllowed = 3 // Allow at least 3 files (e.g. implementation + test + doc)
		}
		if observedCount > maxAllowed {
			violations = append(violations, Violation{
				Type:             CheckBlastRadius,
				Severity:         "BLOCKING",
				Message:          fmt.Sprintf("observed blast radius %d files exceeds allowed limit %d (predicted: %d)", observedCount, maxAllowed, predictedCount),
				RequiresApproval: true,
			})
		}
	}

	// 2. Goal Scope Path Containment check
	// Every changed file must fall within at least one path in goal.Scope
	if len(goal.Scope) > 0 {
		for _, f := range observedFiles {
			if !isFileInScope(f, goal.Scope) {
				violations = append(violations, Violation{
					Type:             CheckScopeLock,
					Severity:         "BLOCKING",
					Path:             f,
					Message:          fmt.Sprintf("file %q is outside allowed Goal scope %v", f, goal.Scope),
					RequiresApproval: true,
				})
			}
		}
	}

	return violations, nil
}

func isFileInScope(file string, allowedScopes []string) bool {
	normFile := filepath.Clean(file)

	for _, scope := range allowedScopes {
		normScope := filepath.Clean(scope)
		if normScope == "." || normScope == "*" || normScope == "all" {
			return true
		}
		if normFile == normScope || strings.HasPrefix(normFile, normScope+"/") {
			return true
		}
	}
	return false
}
