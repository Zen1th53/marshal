package harness

import (
	"context"
	"fmt"
	"slices"
	"strings"

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

// routerHarnesses are the harness names the router understands, with aliases.
var routerHarnesses = map[string]string{
	"codex": "codex", "claude": "claude-code", "claude-code": "claude-code",
	"opencode": "opencode", "antigravity": "antigravity", "agy": "antigravity",
}

// roleHarnesses lists, per role, the harnesses the router will place it on;
// the first is the role's default.
var roleHarnesses = map[model.Role][]string{
	model.RoleArchitect: {"claude-code", "antigravity"},
	model.RoleDeveloper: {"codex", "antigravity"},
	model.RoleQA:        {"opencode", "codex"},
	model.RoleAppSec:    {"antigravity", "claude-code"},
}

// Route builds an advisory execution plan: goal/task -> fixed role -> harness
// -> native mode -> effort -> tool policy -> context strategy -> verification
// policy. It never names a model: the model is resolved at dispatch from the
// operator's execution preference or the provider's own default, and a router
// that invented one would present a guess as a selection.
func (r *ULTRARouter) Route(ctx context.Context, req model.ULTRARouteRequest) (model.ULTRARoutePlan, error) {
	plan := model.ULTRARoutePlan{
		TaskID: req.TaskID,
		Role:   req.FixedRole,
	}

	// 1. Select harness based on fixed role and preference
	candidates, ok := roleHarnesses[req.FixedRole]
	if !ok {
		candidates = []string{"codex"}
	}
	plan.Harness = candidates[0]
	if req.PreferredHarness != "" {
		preferred, known := routerHarnesses[strings.ToLower(req.PreferredHarness)]
		if !known {
			return model.ULTRARoutePlan{}, fmt.Errorf("%w: unknown harness %q (known: codex, claude, opencode, antigravity)", model.ErrInvalid, req.PreferredHarness)
		}
		if slices.Contains(candidates, preferred) {
			plan.Harness = preferred
		} else {
			plan.PreferenceNote = fmt.Sprintf("preference %s is not used for role %s, which runs on %s; routed to %s",
				req.PreferredHarness, req.FixedRole, strings.Join(candidates, " or "), plan.Harness)
		}
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
	plan.UseSubagents = req.MultipleDecoupled

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
	plan.Explanation = fmt.Sprintf("%s selected for %s (native %s); suggested effort %s due to %s; the model is resolved at dispatch.",
		plan.Harness, plan.Role, plan.NativeMode, plan.ReasoningEffort, riskDetail)
	if plan.PreferenceNote != "" {
		plan.Explanation += " Note: " + plan.PreferenceNote + "."
	}
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
