package harness_test

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/harness"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestNoInventedConfigOnVersionDrift(t *testing.T) {
	intel := harness.NewIntelligence()
	profiles := intel.DefaultProfiles()
	codexProf := profiles["codex"]

	// Known knob returns native
	if status := intel.AuditKnob(codexProf, "reasoning_effort"); status != model.StatusNative {
		t.Fatalf("expected native for reasoning_effort, got %v", status)
	}

	// Unknown or invented knob returns probe_required, never invented blindly
	if status := intel.AuditKnob(codexProf, "experimental_auto_commit_and_push"); status != model.StatusProbeRequired {
		t.Fatalf("expected probe_required for unknown knob, got %v", status)
	}

	// Dangerous bypass approvals knob is explicitly rejected as unsupported under MARSHAL authority
	if status := intel.AuditKnob(codexProf, "dangerously_bypass_approvals"); status != model.StatusUnsupported {
		t.Fatalf("expected unsupported for bypass approvals knob, got %v", status)
	}

	// Detect version drift
	hasDrift, msg := intel.DetectDrift(codexProf, "0.29.0-alpha")
	if !hasDrift {
		t.Fatalf("expected version drift detected, got false")
	}
	if msg == "" {
		t.Fatalf("expected drift explanation, got empty string")
	}
}

func TestSameRoleDifferentHarnessRouting(t *testing.T) {
	ctx := context.Background()
	router := harness.NewULTRARouter(nil)

	// Developer role routed to Codex
	reqCodex := model.ULTRARouteRequest{
		GoalID:           "goal-1",
		TaskID:           "task-1",
		FixedRole:        model.RoleDeveloper,
		PreferredHarness: "codex",
		Risk:             model.R1,
	}
	plan1, err := router.Route(ctx, reqCodex)
	if err != nil {
		t.Fatalf("route codex failed: %v", err)
	}
	if plan1.Role != model.RoleDeveloper || plan1.Harness != "codex" || plan1.Model != "gpt-4o" {
		t.Fatalf("unexpected plan1: %+v", plan1)
	}

	// Same Developer role routed to Antigravity without altering role lease
	reqAGY := model.ULTRARouteRequest{
		GoalID:           "goal-1",
		TaskID:           "task-2",
		FixedRole:        model.RoleDeveloper,
		PreferredHarness: "antigravity",
		Risk:             model.R1,
	}
	plan2, err := router.Route(ctx, reqAGY)
	if err != nil {
		t.Fatalf("route antigravity failed: %v", err)
	}
	if plan2.Role != model.RoleDeveloper || plan2.Harness != "antigravity" || plan2.Model != "gemini-2.5-pro" {
		t.Fatalf("unexpected plan2: %+v", plan2)
	}
}

func TestHighRiskIncreasesReasoningAndVerificationPolicy(t *testing.T) {
	ctx := context.Background()
	router := harness.NewULTRARouter(nil)

	req := model.ULTRARouteRequest{
		GoalID:            "goal-crit",
		TaskID:            "task-crit",
		FixedRole:         model.RoleDeveloper,
		Risk:              model.R2,
		HasCriticalClaims: true,
		Scope:             []string{"internal/auth", "internal/secrets"},
	}

	plan, err := router.Route(ctx, req)
	if err != nil {
		t.Fatalf("route high risk failed: %v", err)
	}

	if plan.ReasoningEffort != "high" {
		t.Fatalf("expected high reasoning effort for R2 critical claims, got %s", plan.ReasoningEffort)
	}
	if plan.VerificationPolicy != "strict_adversarial_depth" {
		t.Fatalf("expected strict_adversarial_depth verification policy, got %s", plan.VerificationPolicy)
	}
	if plan.Explanation == "" {
		t.Fatalf("expected human explanation in plan, got empty")
	}
}

func TestLowRiskReversibleTaskAvoidsExpensiveModes(t *testing.T) {
	ctx := context.Background()
	router := harness.NewULTRARouter(nil)

	req := model.ULTRARouteRequest{
		GoalID:            "goal-low",
		TaskID:            "task-low",
		FixedRole:         model.RoleDeveloper,
		Risk:              model.R0,
		HasCriticalClaims: false,
		Scope:             []string{"docs/readme.md"},
	}

	plan, err := router.Route(ctx, req)
	if err != nil {
		t.Fatalf("route low risk failed: %v", err)
	}

	if plan.ReasoningEffort != "none" {
		t.Fatalf("expected none reasoning effort for R0 task, got %s", plan.ReasoningEffort)
	}
	if plan.ToolPolicy != "read_only" {
		t.Fatalf("expected read_only tool policy for R0 doc task, got %s", plan.ToolPolicy)
	}
}

func TestNativeSubagentsOnlyWhenDecoupled(t *testing.T) {
	ctx := context.Background()
	router := harness.NewULTRARouter(nil)

	// Decoupled task benefits from subagents
	reqDecoupled := model.ULTRARouteRequest{
		GoalID:            "goal-dec",
		TaskID:            "task-dec",
		FixedRole:         model.RoleArchitect,
		MultipleDecoupled: true,
		Risk:              model.R1,
	}
	plan1, err := router.Route(ctx, reqDecoupled)
	if err != nil {
		t.Fatalf("route decoupled failed: %v", err)
	}
	if !plan1.UseSubagents {
		t.Fatalf("expected UseSubagents = true for multiple decoupled components")
	}

	// Single linear task avoids subagent overhead
	reqLinear := model.ULTRARouteRequest{
		GoalID:            "goal-lin",
		TaskID:            "task-lin",
		FixedRole:         model.RoleArchitect,
		MultipleDecoupled: false,
		Risk:              model.R1,
	}
	plan2, err := router.Route(ctx, reqLinear)
	if err != nil {
		t.Fatalf("route linear failed: %v", err)
	}
	if plan2.UseSubagents {
		t.Fatalf("expected UseSubagents = false for linear task")
	}
}

func TestProfileFreshnessPolicy(t *testing.T) {
	now := time.Now().UTC()
	prof := model.HarnessProfile{
		Harness:          "opencode",
		InstalledVersion: "0.14.2",
		ProbedAt:         now,
		ExpiresAt:        now.Add(2 * time.Hour),
	}

	if !prof.IsFresh(now.Add(1 * time.Hour)) {
		t.Fatalf("expected fresh at +1h")
	}
	if prof.IsFresh(now.Add(3 * time.Hour)) {
		t.Fatalf("expected expired at +3h")
	}
}
