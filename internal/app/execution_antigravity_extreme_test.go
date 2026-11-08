//go:build !governed

package app

import (
	"errors"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"testing"
)

func TestGovernedAntigravityRefusesUnsupportedScopeBeforeAdmission(t *testing.T) {
	runtime := runtimeForPlan(t)
	for _, kind := range []string{"missing_model", "native_turn", "protected_configuration"} {
		t.Run(kind, func(t *testing.T) {
			task := execution.TaskExecution{TaskID: "task", CanonicalTaskID: "canonical", AssignedModel: "gemini-3.8-flash-high"}
			switch kind {
			case "missing_model":
				task.AssignedModel = ""
			case "native_turn":
				task.NativeTurn = &execution.NativeTurnBinding{Provider: "agy", TurnID: "live"}
			case "protected_configuration":
				task.TargetFiles = []string{".agents/mcp_config.json"}
			}
			pkg, err := execution.BuildConstraintPackage(planGoal(), task, plan.ExecutionPlan{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := runtime.executeProcess05Antigravity(t.Context(), task, pkg, runtime.layout.Root)
			if err == nil || result.Success || len(result.EvidenceList) != 0 {
				t.Fatal("unsupported AGY scope manufactured authority/evidence")
			}
			if kind != "missing_model" && !errors.Is(err, model.ErrUnavailable) {
				t.Fatalf("unexpected boundary refusal %v", err)
			}
		})
	}
}
func TestGovernedAntigravityRequiresExactEligiblePrincipal(t *testing.T) {
	runtime := runtimeForPlan(t)
	agent, err := runtime.RegisterAgent(t.Context(), RegisterAgentRequest{Name: "AGY worker", Role: model.RoleDeveloper, ModelProvider: "agy"})
	if err != nil {
		t.Fatal(err)
	}
	if id, err := runtime.process05AntigravityAgent(t.Context(), execution.TaskExecution{AssignedAgent: agent.ID}); err != nil || id != agent.ID {
		t.Fatal("exact AGY principal lost")
	}
	if _, err := runtime.process05AntigravityAgent(t.Context(), execution.TaskExecution{AssignedAgent: "missing"}); err == nil {
		t.Fatal("missing exact principal fell back")
	}
	wrong, err := runtime.RegisterAgent(t.Context(), RegisterAgentRequest{Name: "other", Role: model.RoleDeveloper, ModelProvider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.process05AntigravityAgent(t.Context(), execution.TaskExecution{AssignedAgent: wrong.ID}); err == nil {
		t.Fatal("foreign exact provider fell back")
	}
}
