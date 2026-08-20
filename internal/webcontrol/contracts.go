package webcontrol

import (
	"time"
)

const (
	DefaultPageLimit = 50
	MaxPageLimit     = 100
)

type SystemHealthState string

const (
	HealthReady       SystemHealthState = "READY"
	HealthDegraded    SystemHealthState = "DEGRADED"
	HealthUnavailable SystemHealthState = "UNAVAILABLE"
	HealthNotRun      SystemHealthState = "NOT_RUN"
)

type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusReady     TaskStatus = "ready"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCanceled  TaskStatus = "canceled"
)

type PagedResponse[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	Total      int    `json:"total"`
	Limit      int    `json:"limit"`
}

func NewPagedResponse[T any](items []T, nextCursor string, total, requestedLimit int) PagedResponse[T] {
	limit := requestedLimit
	if limit <= 0 {
		limit = DefaultPageLimit
	}
	if limit > MaxPageLimit {
		limit = MaxPageLimit
	}
	return PagedResponse[T]{
		Items:      items,
		NextCursor: nextCursor,
		Total:      total,
		Limit:      limit,
	}
}

type MutationEnvelope[T any] struct {
	ExpectedRevision int    `json:"expected_revision,omitempty"`
	IdempotencyKey   string `json:"idempotency_key,omitempty"`
	Payload          T      `json:"payload"`
}

type SystemStatusDTO struct {
	State          SystemHealthState `json:"state"`
	Version        string            `json:"version"`
	CommitSHA      string            `json:"commit_sha"`
	DatabaseSchema string            `json:"database_schema"`
	ActiveWorkers  int               `json:"active_workers"`
	PendingTasks   int               `json:"pending_tasks"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

type AdapterSummaryDTO struct {
	Name         string            `json:"name"`
	BinaryName   string            `json:"binary_name"`
	Installed    bool              `json:"installed"`
	State        SystemHealthState `json:"state"`
	Version      string            `json:"version,omitempty"`
	ProbedAt     time.Time         `json:"probed_at"`
}

type AgentSummaryDTO struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Role               string    `json:"role,omitempty"`
	Provider           string    `json:"provider,omitempty"`
	Model              string    `json:"model,omitempty"`
	Status             string    `json:"status"`
	Capabilities       []string  `json:"capabilities,omitempty"`
	CurrentTaskID      string    `json:"current_task_id,omitempty"`
	CompletedTaskCount int       `json:"completed_task_count,omitempty"`
	LastHeartbeat      time.Time `json:"last_heartbeat,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

type TaskSummaryDTO struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Status      TaskStatus `json:"status"`
	Risk        string     `json:"risk"`
	AssignedTo  string     `json:"assigned_to,omitempty"`
	BaseCommit  string     `json:"base_commit"`
	HeadCommit  string     `json:"head_commit"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type MemoryRecordDTO struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"project_id"`
	Scope      string    `json:"scope"`
	ScopeID    string    `json:"scope_id"`
	Kind       string    `json:"kind"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	Lifecycle  string    `json:"lifecycle"`
	Authority  string    `json:"authority"`
	Confidence float64   `json:"confidence"`
	ObservedAt time.Time `json:"observed_at"`
}

type AuditEventDTO struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	TargetID  string    `json:"target_id"`
	Outcome   string    `json:"outcome"`
}
