package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestRegisterAgentRoleIsImmutable(t *testing.T) {
	st := projectStore(t)
	agent := model.Agent{
		ID: "AGENT-one", ProjectID: "PROJECT-local", DisplayName: "one",
		Role: model.RoleDeveloper, Status: model.AgentRegistered,
	}
	if err := st.RegisterAgent(context.Background(), agent); err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterAgent(context.Background(), agent); err != nil {
		t.Fatalf("idempotent registration: %v", err)
	}
	agent.Role = model.RoleAppSec
	if err := st.RegisterAgent(context.Background(), agent); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("role mutation error = %v", err)
	}
}

func TestRegisterAgentStoresEmptyCapabilitiesAsArray(t *testing.T) {
	st := projectStore(t)
	agent := model.Agent{
		ID: "AGENT-empty", ProjectID: "PROJECT-local", DisplayName: "empty",
		Role: model.RoleDeveloper, Status: model.AgentRegistered,
	}
	if err := st.RegisterAgent(context.Background(), agent); err != nil {
		t.Fatal(err)
	}
	var capabilities string
	if err := st.db.QueryRow("SELECT capabilities_json FROM agents WHERE agent_id = ?", agent.ID).Scan(&capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities != "[]" {
		t.Fatalf("capabilities_json = %q, want []", capabilities)
	}
}

func TestSessionBindsStoredRoleAndPersistsHeartbeat(t *testing.T) {
	st := projectStore(t)
	agent, session := activeDeveloper(t, st, "session")
	if session.AgentID != agent.ID || session.Role != model.RoleDeveloper {
		t.Fatalf("session binding = %#v", session)
	}
	heartbeat := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	if err := st.Heartbeat(context.Background(), session.ID, heartbeat, 0); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastHeartbeat.Equal(heartbeat) || got.Revision != 1 {
		t.Fatalf("heartbeat session = %#v", got)
	}
	if err := st.TerminateSession(context.Background(), session.ID, model.SessionTerminated, 0); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale termination error = %v", err)
	}
	if err := st.TerminateSession(context.Background(), session.ID, model.SessionTerminated, 1); err != nil {
		t.Fatal(err)
	}
}

func TestAgentLifecycleCRUD(t *testing.T) {
	st := projectStore(t)
	ctx := context.Background()

	agent := model.Agent{
		ID:            "AGENT-crud-test",
		ProjectID:     "PROJECT-local",
		DisplayName:   "Original Name",
		Role:          model.RoleDeveloper,
		ModelProvider: "claude",
		ModelName:     "claude-3-7-sonnet",
		Capabilities:  []string{"code_edit"},
		Status:        model.AgentRegistered,
	}

	if err := st.RegisterAgent(ctx, agent); err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}

	got, err := st.GetAgent(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if got.DisplayName != "Original Name" || got.Revision != 0 {
		t.Fatalf("unexpected agent after register: %+v", got)
	}

	// Update with wrong revision -> conflict
	updateReq := got
	updateReq.DisplayName = "Updated Name"
	updateReq.Status = model.AgentActive
	updateReq.Capabilities = []string{"code_edit", "dag_plan"}
	if _, err := st.UpdateAgent(ctx, updateReq, 99); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("expected conflict on wrong revision update, got: %v", err)
	}

	// Valid update
	updated, err := st.UpdateAgent(ctx, updateReq, 0)
	if err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}
	if updated.DisplayName != "Updated Name" || updated.Revision != 1 || updated.Status != model.AgentActive {
		t.Fatalf("unexpected updated agent: %+v", updated)
	}

	// Delete with wrong revision -> conflict
	if err := st.DeleteAgent(ctx, agent.ID, 0); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("expected conflict on wrong revision delete, got: %v", err)
	}

	// Valid delete
	if err := st.DeleteAgent(ctx, agent.ID, 1); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}

	// Get after delete -> not found
	if _, err := st.GetAgent(ctx, agent.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("expected not found after delete, got: %v", err)
	}
}
