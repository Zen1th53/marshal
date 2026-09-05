package harness

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
)

// ULTRARouter upgrades MARSHAL routing to full-stack native operational optimization.
type ULTRARouter struct {
	intelligence *Intelligence
}

func NewULTRARouter(intel *Intelligence) *ULTRARouter {
	if intel == nil {
		intel = NewIntelligence()
	}
	return &ULTRARouter{
		intelligence: intel,
	}
}

// Route builds the optimal execution plan across the entire pipeline:
// Goal/task -> fixed role -> harness -> model -> native mode/settings -> tool policy -> context strategy -> verification policy
func (r *ULTRARouter) Route(ctx context.Context, req model.ULTRARouteRequest) (model.ULTRARoutePlan, error) {
	profiles := r.intelligence.DefaultProfiles()

	plan := model.ULTRARoutePlan{
		TaskID: req.TaskID,
		Role:   req.FixedRole,
	}

	// 1. Select harness based on fixed role and preference
	switch req.FixedRole {
	case model.RoleArchitect:
		if req.PreferredHarness == "antigravity" {
			plan.Harness = "antigravity"
			plan.Model = "gemini-2.5-pro"
		} else {
			plan.Harness = "claude-code"
			plan.Model = "claude-3-7-sonnet"
		}
	case model.RoleDeveloper:
		if req.PreferredHarness == "antigravity" {
			plan.Harness = "antigravity"
			plan.Model = "gemini-2.5-pro"
		} else {
			plan.Harness = "codex"
			if (req.Risk == model.R2 || req.Risk == model.R3 || req.HasCriticalClaims) && contains(profiles["codex"].SupportedModels, "o1") {
				plan.Model = "o1"
			} else {
				plan.Model = "gpt-4o"
			}
		}
	case model.RoleQA:
		if req.PreferredHarness == "codex" {
			plan.Harness = "codex"
			plan.Model = "gpt-4o"
		} else {
			plan.Harness = "opencode"
			plan.Model = "deepseek-coder"
		}
	case model.RoleAppSec:
		if req.PreferredHarness == "claude-code" {
			plan.Harness = "claude-code"
			plan.Model = "claude-3-7-sonnet"
		} else {
			plan.Harness = "antigravity"
			plan.Model = "gemini-2.5-pro"
		}
	default:
		plan.Harness = "codex"
		plan.Model = "gpt-4o"
	}

	// 2. Select native mode based on selected harness
	switch plan.Harness {
	case "codex":
		plan.NativeMode = "non_interactive"
	case "claude-code":
		plan.NativeMode = "structured_events"
	case "opencode":
		plan.NativeMode = "code_mode"
	case "antigravity":
		plan.NativeMode = "headless_worker"
	}

	// 3. Reasoning effort controls scaled by risk and critical claims
	isHighRiskOrCritical := (req.Risk == model.R2 || req.Risk == model.R3 || req.HasCriticalClaims)
	if isHighRiskOrCritical {
		plan.ReasoningEffort = "high"
		plan.VerificationPolicy = "strict_adversarial_depth"
		plan.ContextStrategy = "focused_with_critical_claim_deps"
	} else if req.Risk == model.R0 {
		plan.ReasoningEffort = "none"
		plan.VerificationPolicy = "standard_fast"
		plan.ContextStrategy = "minimal_local_context"
	} else {
		plan.ReasoningEffort = "medium"
		plan.VerificationPolicy = "standard_fast"
		plan.ContextStrategy = "standard_working_set"
	}

	// 4. Subagents used only when task structure benefits
	if req.MultipleDecoupled {
		plan.UseSubagents = true
	} else {
		plan.UseSubagents = false
	}

	// 5. Tool policy
	if req.Risk == model.R0 {
		plan.ToolPolicy = "read_only"
	} else {
		plan.ToolPolicy = "strict_sandboxed"
	}

	// 6. Concise human-readable explanation for TUI
	riskDetail := string(req.Risk)
	if req.HasCriticalClaims {
		riskDetail += " with critical claims"
	}
	plan.Explanation = fmt.Sprintf("%s selected for %s: %s model + native %s; effort=%s due to %s.",
		plan.Harness, plan.Role, plan.Model, plan.NativeMode, plan.ReasoningEffort, riskDetail)

	return plan, nil
}

func contains(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}
