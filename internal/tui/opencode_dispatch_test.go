package tui

import (
	"github.com/Zen1th53/marshal/internal/model"
	"testing"
)

func TestGovernedOpenCodeDispatchRequiresMatchingDeveloper(t *testing.T) {
	good := model.Agent{ID: "agent", Role: model.RoleDeveloper, ModelProvider: "opencode", Status: model.AgentRegistered}
	if err := validateOpenCodeDispatchAgent(good); err != nil {
		t.Fatal(err)
	}
	bad := []model.Agent{good, good, good}
	bad[0].Role = model.RoleQA
	bad[1].ModelProvider = "codex"
	bad[2].Status = model.AgentDisabled
	for _, agent := range bad {
		if validateOpenCodeDispatchAgent(agent) == nil {
			t.Fatalf("invalid principal accepted: %+v", agent)
		}
	}
}
