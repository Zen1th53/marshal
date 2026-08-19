package model

import "time"

type Role string

const (
	RoleOrchestrator Role = "orchestrator"
	RoleArchitect    Role = "architect"
	RoleDeveloper    Role = "developer"
	RoleQA           Role = "qa"
	RoleAppSec       Role = "appsec"
	RoleReviewer     Role = "reviewer"
	RoleAdmin        Role = "admin"
)

type AgentStatus string

const (
	AgentRegistered AgentStatus = "registered"
	AgentActive     AgentStatus = "active"
	AgentDisabled   AgentStatus = "disabled"
)

type Agent struct {
	ID            string      `json:"id"`
	ProjectID     string      `json:"project_id"`
	DisplayName   string      `json:"display_name"`
	Role          Role        `json:"role"`
	ModelProvider string      `json:"model_provider,omitempty"`
	ModelName     string      `json:"model_name,omitempty"`
	Capabilities  []string    `json:"capabilities"`
	Status        AgentStatus `json:"status"`
	Revision      int64       `json:"revision"`
	CreatedAt     time.Time   `json:"created_at"`
}

type SessionStatus string

const (
	SessionActive     SessionStatus = "active"
	SessionStale      SessionStatus = "stale"
	SessionFailed     SessionStatus = "failed"
	SessionTerminated SessionStatus = "terminated"
)

type SessionStart struct {
	ID        string
	AgentID   string
	ProjectID string
	Branch    string
	Worktree  string
}

type Session struct {
	ID            string        `json:"id"`
	AgentID       string        `json:"agent_id"`
	ProjectID     string        `json:"project_id"`
	TaskID        *string       `json:"task_id,omitempty"`
	Role          Role          `json:"role"`
	Branch        string        `json:"branch,omitempty"`
	Worktree      string        `json:"worktree,omitempty"`
	StartedAt     time.Time     `json:"started_at"`
	LastHeartbeat time.Time     `json:"last_heartbeat"`
	Status        SessionStatus `json:"status"`
	Revision      int64         `json:"revision"`
}

func (r Role) Valid() bool {
	switch r {
	case RoleOrchestrator, RoleArchitect, RoleDeveloper, RoleQA, RoleAppSec, RoleReviewer, RoleAdmin:
		return true
	default:
		return false
	}
}
