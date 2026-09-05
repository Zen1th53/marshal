package interpretation

import (
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

// Scaler calculates the required interpretation diversity based on task risk and scope.
type Scaler struct{}

func NewScaler() *Scaler {
	return &Scaler{}
}

// EvaluateRequirements determines whether a task requires single or multi-agent blind interpretation.
func (s *Scaler) EvaluateRequirements(risk model.Risk, isDestructive bool, scope []string, tags []string) model.InterpretationRequirement {
	// Check for high-sensitivity keywords in tags, scope, or context
	isCritical := false
	for _, tag := range tags {
		norm := strings.ToLower(tag)
		if norm == "auth" || norm == "migration" || norm == "delete" ||
			norm == "secrets" || norm == "privilege" || norm == "production" || norm == "public-api" {
			isCritical = true
			break
		}
	}

	for _, p := range scope {
		norm := strings.ToLower(p)
		if strings.Contains(norm, "secret") || strings.Contains(norm, "auth") ||
			strings.Contains(norm, "migration") || strings.Contains(norm, "credential") {
			isCritical = true
			break
		}
	}

	// Critical R3 or sensitive scope: requires >=2 independent interpreters from different harnesses/models
	if risk == model.R3 || isCritical {
		return model.InterpretationRequirement{
			MinInterpreters:             2,
			RequireHeterogeneousHarness: true,
			RequireDifferentModels:      true,
			Reason:                      "Critical R3 / sensitive operations require multi-agent heterogeneous blind interpretation",
		}
	}

	// R2 or ambiguous destructive request: requires >= 2 independent interpretations
	if risk == model.R2 || isDestructive {
		return model.InterpretationRequirement{
			MinInterpreters:             2,
			RequireHeterogeneousHarness: false,
			RequireDifferentModels:      false,
			Reason:                      "R2 or destructive operation requires >=2 independent interpretations to prevent misaligned acceleration",
		}
	}

	// Low-risk reversible task (R0 / R1): single interpretation is sufficient. No multi-agent tax.
	return model.InterpretationRequirement{
		MinInterpreters:             1,
		RequireHeterogeneousHarness: false,
		RequireDifferentModels:      false,
		Reason:                      "Low-risk reversible task proceeds without multi-agent overhead",
	}
}
