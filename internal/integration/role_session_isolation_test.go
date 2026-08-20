package integration

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestRoleSessionIsolationAcrossRoles(t *testing.T) {
	ctx := context.Background()
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	runtime, err := app.OpenWithOptions(ctx, repo.Path(), app.Options{
		Adapters: map[string]adapter.Adapter{"fake": fakeCommitAdapter{}},
	})
	if err != nil {
		t.Fatalf("OpenWithOptions: %v", err)
	}
	defer runtime.Close()

	// 1. Register agents for distinct roles
	roles := []model.Role{
		model.RoleOrchestrator,
		model.RoleArchitect,
		model.RoleDeveloper,
		model.RoleQA,
		model.RoleAppSec,
	}

	agentIDs := make(map[model.Role]string)
	for _, role := range roles {
		agent, err := runtime.RegisterAgent(ctx, app.RegisterAgentRequest{
			Name: "agent-" + string(role),
			Role: role,
		})
		if err != nil {
			t.Fatalf("RegisterAgent for role %s: %v", role, err)
		}
		agentIDs[role] = agent.ID
	}

	// 2. Import task
	taskID := "TASK-ROLE-ISO-001"
	if _, err := runtime.ImportTasks(ctx, []model.Task{
		{
			ID:     taskID,
			Title:  "Role Isolation Test Task",
			Status: model.TaskReady,
			Risk:   model.R1,
		},
	}); err != nil {
		t.Fatalf("ImportTasks: %v", err)
	}

	// 3. Verify Developer claims task in an independent, fresh session
	devClaim, err := runtime.Claim(ctx, app.ClaimRequest{
		TaskID:           taskID,
		AgentID:          agentIDs[model.RoleDeveloper],
		ExpectedRevision: 0,
	})
	if err != nil {
		t.Fatalf("Developer claim: %v", err)
	}

	if devClaim.Session.Role != model.RoleDeveloper {
		t.Fatalf("expected developer session role, got %s", devClaim.Session.Role)
	}

	// 4. Release task from Developer
	if err := runtime.Release(ctx, app.ReleaseRequest{TaskID: taskID}); err != nil {
		t.Fatalf("ReleaseTask: %v", err)
	}

	taskAfterDev, err := runtime.Task(ctx, taskID)
	if err != nil {
		t.Fatalf("runtime.Task: %v", err)
	}

	// 5. Claim by QA Lead - must generate a brand new session ID with QA role
	qaClaim, err := runtime.Claim(ctx, app.ClaimRequest{
		TaskID:           taskID,
		AgentID:          agentIDs[model.RoleQA],
		ExpectedRevision: taskAfterDev.Revision,
	})
	if err != nil {
		t.Fatalf("QA claim: %v", err)
	}

	if qaClaim.Session.ID == devClaim.Session.ID {
		t.Fatalf("CRITICAL ISOLATION VIOLATION: QA reused Developer session ID %s", devClaim.Session.ID)
	}
	if qaClaim.Session.Role != model.RoleQA {
		t.Fatalf("expected QA session role, got %s", qaClaim.Session.Role)
	}

	// 6. Release task from QA
	if err := runtime.Release(ctx, app.ReleaseRequest{TaskID: taskID}); err != nil {
		t.Fatalf("ReleaseTask QA: %v", err)
	}

	taskAfterQA, err := runtime.Task(ctx, taskID)
	if err != nil {
		t.Fatalf("runtime.Task: %v", err)
	}

	// 7. Claim by AppSec - must generate another independent session ID with AppSec role
	appSecClaim, err := runtime.Claim(ctx, app.ClaimRequest{
		TaskID:           taskID,
		AgentID:          agentIDs[model.RoleAppSec],
		ExpectedRevision: taskAfterQA.Revision,
	})
	if err != nil {
		t.Fatalf("AppSec claim: %v", err)
	}

	if appSecClaim.Session.ID == devClaim.Session.ID || appSecClaim.Session.ID == qaClaim.Session.ID {
		t.Fatalf("CRITICAL ISOLATION VIOLATION: AppSec reused prior session ID %s", appSecClaim.Session.ID)
	}
	if appSecClaim.Session.Role != model.RoleAppSec {
		t.Fatalf("expected AppSec session role, got %s", appSecClaim.Session.Role)
	}
}
