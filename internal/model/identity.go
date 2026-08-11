package model

import "time"

type Role string

const (
	RoleOrchestrator Role = "orchestrator"
	RoleArchitect    Role = "architect"
	RoleDeveloper    Role = "developer"
	RoleQA           Role = "qa"
	RoleAppSec       Role = "appsec"
)

type AgentStatus string

const (
	AgentRegistered AgentStatus = "registered"
	AgentActive     AgentStatus = "active"
	AgentDisabled   AgentStatus = "disabled"
)

type Agent struct {
	ID            string
	ProjectID     string
	DisplayName   string
	Role          Role
	ModelProvider string
	ModelName     string
	Capabilities  []string
	Status        AgentStatus
	Revision      int64
	CreatedAt     time.Time
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
	ID            string
	AgentID       string
	ProjectID     string
	TaskID        *string
	Role          Role
	Branch        string
	Worktree      string
	StartedAt     time.Time
	LastHeartbeat time.Time
	Status        SessionStatus
	Revision      int64
}

func (r Role) Valid() bool {
	switch r {
	case RoleOrchestrator, RoleArchitect, RoleDeveloper, RoleQA, RoleAppSec:
		return true
	default:
		return false
	}
}
