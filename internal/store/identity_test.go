package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/slaves/internal/model"
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
