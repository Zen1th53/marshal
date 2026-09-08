package execution

import (
	"errors"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// Common execution errors.
var (
	ErrRunNotFound             = errors.New("execution run not found")
	ErrRunConflict             = errors.New("execution run conflict")
	ErrRunInvalid              = errors.New("execution run invalid")
	ErrRunBlocked              = errors.New("execution run blocked")
	ErrInvalidStateTransition  = errors.New("invalid state transition")
	ErrLeaseConflict           = errors.New("execution lease conflict")
	ErrLeaseExpired            = errors.New("execution lease expired")
	ErrUnauthorizedWorker      = errors.New("unauthorized worker")
	ErrApprovalRequired        = errors.New("hard approval required")
	ErrApprovalDenied          = errors.New("approval denied")
	ErrApprovalTOCTOUViolation = errors.New("approval TOCTOU violation: action or state has changed")
	ErrConstraintViolation     = errors.New("constraint violation")
	ErrDriftDetected           = errors.New("execution alignment drift detected")
	ErrBudgetExhausted         = errors.New("execution budget exhausted")
	ErrCheckpointFailed        = errors.New("checkpoint creation or restoration failed")
	ErrRetryLoopExceeded       = errors.New("retry limit or ping-pong loop exceeded")
	ErrEvidenceMissing         = errors.New("required verification evidence missing")
	ErrIsolationCompromised    = errors.New("filesystem or worktree isolation compromised")
)

// RunState represents the lifecycle state of an ExecutionRun.
type RunState string

const (
	RunReady                   RunState = "READY"
	RunRunning                 RunState = "RUNNING"
	RunPaused                  RunState = "PAUSED"
	RunNeedsApproval           RunState = "NEEDS_APPROVAL"
	RunBlocked                 RunState = "BLOCKED"
	RunCancelling              RunState = "CANCELLING"
	RunCancelled               RunState = "CANCELLED"
	RunFailed                  RunState = "FAILED"
	RunDonePendingVerification RunState = "DONE_PENDING_VERIFICATION"
)

// IsTerminal reports whether the run state is terminal.
func (s RunState) IsTerminal() bool {
	return s == RunCancelled || s == RunFailed || s == RunDonePendingVerification
}

// RunPhase represents the current phase of the execution lifecycle.
type RunPhase string

const (
	PhaseInit             RunPhase = "INIT"
	PhaseScheduling       RunPhase = "SCHEDULING"
	PhaseExecuting        RunPhase = "EXECUTING"
	PhasePaused           RunPhase = "PAUSED"
	PhaseAwaitingApproval RunPhase = "AWAITING_APPROVAL"
	PhaseRecovering       RunPhase = "RECOVERING"
	PhaseCompletedPending RunPhase = "COMPLETED_PENDING_VERIFY"
	PhaseTerminated       RunPhase = "TERMINATED"
)

// TaskExecutionState represents the state of an individual task during execution.
type TaskExecutionState string

const (
	TaskPending                TaskExecutionState = "PENDING"
	TaskReady                  TaskExecutionState = "READY"
	TaskAssigned               TaskExecutionState = "ASSIGNED"
	TaskRunning                TaskExecutionState = "RUNNING"
	TaskWaitingTool            TaskExecutionState = "WAITING_TOOL"
	TaskWaitingAgent           TaskExecutionState = "WAITING_AGENT"
	TaskNeedsApproval          TaskExecutionState = "NEEDS_APPROVAL"
	TaskBlocked                TaskExecutionState = "BLOCKED"
	TaskPaused                 TaskExecutionState = "PAUSED"
	TaskFailed                 TaskExecutionState = "FAILED"
	TaskCompletedPendingVerify TaskExecutionState = "COMPLETED_PENDING_VERIFY"
	TaskCancelled              TaskExecutionState = "CANCELLED"
)

// IsTerminal reports whether the task execution state is terminal.
func (s TaskExecutionState) IsTerminal() bool {
	return s == TaskCompletedPendingVerify || s == TaskFailed || s == TaskCancelled || s == TaskBlocked
}

// LeaseStatus represents the state of a resource lease.
type LeaseStatus string

const (
	LeaseActive   LeaseStatus = "ACTIVE"
	LeaseExpired  LeaseStatus = "EXPIRED"
	LeaseReleased LeaseStatus = "RELEASED"
	LeaseRevoked  LeaseStatus = "REVOKED"
)

// ApprovalStatus represents the state of a runtime approval request.
type ApprovalStatus string

const (
	ApprovalRequested   ApprovalStatus = "REQUESTED"
	ApprovalApproved    ApprovalStatus = "APPROVED"
	ApprovalDenied      ApprovalStatus = "DENIED"
	ApprovalExpired     ApprovalStatus = "EXPIRED"
	ApprovalInvalidated ApprovalStatus = "INVALIDATED"
)

// EvidenceStatus represents the freshness and validity of an evidence record.
type EvidenceStatus string

const (
	EvidenceValid       EvidenceStatus = "VALID"
	EvidenceStale       EvidenceStatus = "STALE"
	EvidenceInvalidated EvidenceStatus = "INVALIDATED"
)

// ClaimStatus represents the epistemic status of an agent claim during execution.
type ClaimStatus string

const (
	ClaimUnsupported ClaimStatus = "UNSUPPORTED"
	ClaimSupported   ClaimStatus = "SUPPORTED"
	ClaimVerified    ClaimStatus = "VERIFIED"
	ClaimContested   ClaimStatus = "CONTESTED"
	ClaimStale       ClaimStatus = "STALE"
	ClaimInvalidated ClaimStatus = "INVALIDATED"
)

// IntentStage represents crash consistency two-phase intent lifecycle stages.
type IntentStage string

const (
	IntentPrepared       IntentStage = "PREPARED"
	IntentStarted        IntentStage = "STARTED"
	IntentEffectObserved IntentStage = "EFFECT_OBSERVED"
	IntentCommitted      IntentStage = "COMMITTED"
)

// ExecutionRun represents the durable canonical run entity in Process 05.
type ExecutionRun struct {
	RunID               string                      `json:"run_id"`
	Version             int64                       `json:"version"` // CAS concurrency version
	ProjectID           projectid.ID                `json:"project_id"`
	SessionID           string                      `json:"session_id"`
	GoalID              string                      `json:"goal_id"`
	GoalRevision        int64                       `json:"goal_revision"`
	PlanID              string                      `json:"plan_id"`
	PlanVersion         int64                       `json:"plan_version"`
	State               RunState                    `json:"state"`
	Mode                plan.Mode                   `json:"mode"`
	CurrentPhase        RunPhase                    `json:"current_phase"`
	ConstitutionVersion constitution.Version        `json:"constitution_version"`
	Tasks               map[string]TaskExecution    `json:"tasks"`
	ActiveWorkers       map[string]WorkerDescriptor `json:"active_workers,omitempty"`
	Leases              map[string]Lease            `json:"leases,omitempty"`
	Approvals           map[string]RuntimeApproval  `json:"approvals,omitempty"`
	Checkpoints         []CheckpointRecord          `json:"checkpoints,omitempty"`
	Evidence            []EvidenceRef               `json:"evidence,omitempty"`
	BudgetConsumed      BudgetUsage                 `json:"budget_consumed"`
	ProviderCalls       []ProviderCallMeta          `json:"provider_calls,omitempty"`
	Failures            []RunFailure                `json:"failures,omitempty"`
	Handoffs            []TypedHandoff              `json:"handoffs,omitempty"`
	PolicySnapshot      string                      `json:"policy_snapshot,omitempty"`
	OriginalRequest     string                      `json:"original_request"`
	HardConstraints     []string                    `json:"hard_constraints,omitempty"`
	Provenance          RunProvenance               `json:"provenance"`
	StartedAt           time.Time                   `json:"started_at"`
	UpdatedAt           time.Time                   `json:"updated_at"`
	EndedAt             *time.Time                  `json:"ended_at,omitempty"`
}

// TaskExecution tracks execution state of a task inside a run.
type TaskExecution struct {
	TaskID            string             `json:"task_id"`
	Description       string             `json:"description"`
	AssignedRole      string             `json:"assigned_role"`
	AssignedAgent     string             `json:"assigned_agent"`
	AssignedHarness   string             `json:"assigned_harness"`
	AssignedModel     string             `json:"assigned_model"`
	FallbackHarnesses []string           `json:"fallback_harnesses,omitempty"`
	State             TaskExecutionState `json:"state"`
	Dependencies      []string           `json:"dependencies,omitempty"`
	Mutates           bool               `json:"mutates"`
	TargetFiles       []string           `json:"target_files,omitempty"`
	LeaseID           string             `json:"lease_id,omitempty"`
	WorktreePath      string             `json:"worktree_path,omitempty"`
	RequiredEvidence  []string           `json:"required_evidence,omitempty"`
	CollectedEvidence []string           `json:"collected_evidence,omitempty"`
	ApprovalRequired  bool               `json:"approval_required"`
	ApprovalID        string             `json:"approval_id,omitempty"`
	CheckpointsBefore []string           `json:"checkpoints_before,omitempty"`
	Attempts          int                `json:"attempts"`
	LastFingerprint   string             `json:"last_fingerprint,omitempty"`
	LastFailureReason string             `json:"last_failure_reason,omitempty"`
	OutputArtifacts   []string           `json:"output_artifacts,omitempty"`
	StartedAt         *time.Time         `json:"started_at,omitempty"`
	CompletedAt       *time.Time         `json:"completed_at,omitempty"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

// WorkerDescriptor describes an active worker executing under MARSHAL governance.
type WorkerDescriptor struct {
	AgentID       string    `json:"agent_id"`
	Role          string    `json:"role"`
	Harness       string    `json:"harness"`
	Model         string    `json:"model"`
	TaskID        string    `json:"task_id"`
	LeaseID       string    `json:"lease_id"`
	WorktreePath  string    `json:"worktree_path,omitempty"`
	ProcessID     int       `json:"process_id,omitempty"`
	LaunchedAt    time.Time `json:"launched_at"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
}

// Lease records exclusive ownership for task mutation.
type Lease struct {
	LeaseID            string      `json:"lease_id"`
	RunID              string      `json:"run_id"`
	TaskID             string      `json:"task_id"`
	AgentID            string      `json:"agent_id"`
	Role               string      `json:"role"`
	Status             LeaseStatus `json:"status"`
	AcquiredAt         time.Time   `json:"acquired_at"`
	ExpiresAt          time.Time   `json:"expires_at"`
	HeartbeatAt        time.Time   `json:"heartbeat_at"`
	ScopedResources    []string    `json:"scoped_resources"`
	MutationScope      string      `json:"mutation_scope"`
	WorktreePath       string      `json:"worktree_path,omitempty"`
	TakeoverProvenance string      `json:"takeover_provenance,omitempty"`
}

// RuntimeApproval records a hard gated action requiring explicit human decision.
type RuntimeApproval struct {
	ApprovalID     string         `json:"approval_id"`
	RunID          string         `json:"run_id"`
	TaskID         string         `json:"task_id"`
	PlanID         string         `json:"plan_id"`
	PlanVersion    int64          `json:"plan_version"`
	OperationType  string         `json:"operation_type"`
	TargetResource string         `json:"target_resource"`
	RiskLevel      model.Risk     `json:"risk_level"`
	Scope          string         `json:"scope"`
	DiffPreview    string         `json:"diff_preview,omitempty"`
	ActionDigest   string         `json:"action_digest"`
	StateDigest    string         `json:"state_digest"`
	Status         ApprovalStatus `json:"status"`
	ApprovedBy     string         `json:"approved_by,omitempty"`
	DecisionReason string         `json:"decision_reason,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	ResolvedAt     *time.Time     `json:"resolved_at,omitempty"`
	ExpiresAt      *time.Time     `json:"expires_at,omitempty"`
}

// CheckpointRecord records a durable rollback boundary.
type CheckpointRecord struct {
	CheckpointID string     `json:"checkpoint_id"`
	RunID        string     `json:"run_id"`
	TaskID       string     `json:"task_id"`
	ProjectID    string     `json:"project_id"`
	GitCommit    string     `json:"git_commit"`
	WorktreePath string     `json:"worktree_path,omitempty"`
	StateDigest  string     `json:"state_digest"`
	Reason       string     `json:"reason"`
	DetailsJSON  string     `json:"details_json,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	RestoredAt   *time.Time `json:"restored_at,omitempty"`
}

// TypedHandoff records a structured handover between workers.
type TypedHandoff struct {
	HandoffID       string             `json:"handoff_id"`
	RunID           string             `json:"run_id"`
	TaskID          string             `json:"task_id"`
	FromAgent       string             `json:"from_agent"`
	ToAgent         string             `json:"to_agent"`
	Reason          string             `json:"reason"`
	CurrentState    TaskExecutionState `json:"current_state"`
	CompletedWork   []string           `json:"completed_work,omitempty"`
	RemainingWork   []string           `json:"remaining_work,omitempty"`
	HardConstraints []string           `json:"hard_constraints,omitempty"`
	EvidenceRefs    []string           `json:"evidence_refs,omitempty"`
	Blockers        []string           `json:"blockers,omitempty"`
	CheckpointID    string             `json:"checkpoint_id,omitempty"`
	NextAction      string             `json:"next_action"`
	Timestamp       time.Time          `json:"timestamp"`
}

// BudgetUsage tracks actual measured resource consumption.
type BudgetUsage struct {
	ModelCalls        int     `json:"model_calls"`
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	TotalCostUSD      float64 `json:"total_cost_usd"`
	WallClockMs       int64   `json:"wall_clock_ms"`
	ToolCalls         int     `json:"tool_calls"`
	Retries           int     `json:"retries"`
	Handoffs          int     `json:"handoffs"`
	RateLimitCount    int     `json:"rate_limit_count"`
	RetryAfterSeconds float64 `json:"retry_after_seconds,omitempty"`
}

// ProviderCallMeta records metadata for provider invocation.
type ProviderCallMeta struct {
	CallID       string    `json:"call_id"`
	TaskID       string    `json:"task_id"`
	Harness      string    `json:"harness"`
	Model        string    `json:"model"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	DurationMs   int64     `json:"duration_ms"`
	Timestamp    time.Time `json:"timestamp"`
	HTTPStatus   int       `json:"http_status,omitempty"`
	RetryAfter   string    `json:"retry_after,omitempty"`
}

// RunFailure captures structured failure information.
type RunFailure struct {
	TaskID      string    `json:"task_id,omitempty"`
	Stage       string    `json:"stage"`
	Reason      string    `json:"reason"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	Recoverable bool      `json:"recoverable"`
	Timestamp   time.Time `json:"timestamp"`
}

// RunProvenance captures environment and git identity at run time.
type RunProvenance struct {
	GitCommit   string `json:"git_commit"`
	GitBranch   string `json:"git_branch"`
	RepoRoot    string `json:"repo_root"`
	HostName    string `json:"host_name"`
	MARSHALHash string `json:"marshal_hash,omitempty"`
}

// EvidenceRef references an evidence record.
type EvidenceRef struct {
	EvidenceID   string         `json:"evidence_id"`
	TaskID       string         `json:"task_id"`
	Type         string         `json:"type"`
	Digest       string         `json:"digest"`
	Status       EvidenceStatus `json:"status"`
	CapturedAt   time.Time      `json:"captured_at"`
	WorktreePath string         `json:"worktree_path,omitempty"`
}

// ExecutionEvidence is a raw evidence record from tool execution.
type ExecutionEvidence struct {
	EvidenceID    string         `json:"evidence_id"`
	RunID         string         `json:"run_id"`
	TaskID        string         `json:"task_id"`
	ToolName      string         `json:"tool_name"`
	Args          []string       `json:"args,omitempty"`
	Cwd           string         `json:"cwd"`
	CommandDigest string         `json:"command_digest"`
	ExitCode      int            `json:"exit_code"`
	StdoutSummary string         `json:"stdout_summary"`
	StderrSummary string         `json:"stderr_summary"`
	OutputDigest  string         `json:"output_digest"`
	RawArtifactRef string        `json:"raw_artifact_ref,omitempty"`
	GitCommit     string         `json:"git_commit,omitempty"`
	WorktreePath  string         `json:"worktree_path,omitempty"`
	BinaryPath    string         `json:"binary_path,omitempty"`
	BinaryVersion string         `json:"binary_version,omitempty"`
	BinarySHA256  string         `json:"binary_sha256,omitempty"`
	Status        EvidenceStatus `json:"status"`
	DurationMs    int64          `json:"duration_ms"`
	RelevantFiles []string       `json:"relevant_files,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	StaleAt       *time.Time     `json:"stale_at,omitempty"`
	StaleReason   string         `json:"stale_reason,omitempty"`
}

// ExecutionClaim represents a claim submitted during execution.
type ExecutionClaim struct {
	ClaimID       string      `json:"claim_id"`
	RunID         string      `json:"run_id"`
	TaskID        string      `json:"task_id"`
	AgentID       string      `json:"agent_id"`
	ClaimText     string      `json:"claim_text"`
	Status        ClaimStatus `json:"status"`
	EvidenceRefs  []string    `json:"evidence_refs,omitempty"`
	Contradiction string      `json:"contradiction,omitempty"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

// JournalEvent represents an append-only event in the execution journal.
type JournalEvent struct {
	EventID     int64     `json:"event_id"`
	RunID       string    `json:"run_id"`
	TaskID      string    `json:"task_id,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	Actor       string    `json:"actor"`
	EventType   string    `json:"event_type"`
	StateBefore string    `json:"state_before,omitempty"`
	StateAfter  string    `json:"state_after,omitempty"`
	ActionRef   string    `json:"action_ref,omitempty"`
	Summary     string    `json:"summary"`
	PayloadJSON string    `json:"payload_json,omitempty"`
	Digest      string    `json:"digest,omitempty"`
}

// ExecutionIntent represents a two-phase durable intent for crash consistency.
type ExecutionIntent struct {
	IntentID       string      `json:"intent_id"`
	RunID          string      `json:"run_id"`
	TaskID         string      `json:"task_id"`
	IdempotencyKey string      `json:"idempotency_key"`
	Stage          IntentStage `json:"stage"`
	Operation      string      `json:"operation"`
	DetailsJSON    string      `json:"details_json,omitempty"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

// Process06HandoffBundle contains all information needed for Process 06 verification.
type Process06HandoffBundle struct {
	ProjectID               projectid.ID          `json:"project_id"`
	GoalID                  string                `json:"goal_id"`
	GoalRevision            int64                 `json:"goal_revision"`
	PlanID                  string                `json:"plan_id"`
	PlanVersion             int64                 `json:"plan_version"`
	RunID                   string                `json:"run_id"`
	RunVersion              int64                 `json:"run_version"`
	FinalState              RunState              `json:"final_state"`
	FinalGitTree            string                `json:"final_git_tree"`
	Tasks                   []TaskExecution       `json:"tasks"`
	Claims                  []ExecutionClaim      `json:"claims"`
	EvidenceBundle          []ExecutionEvidence   `json:"evidence_bundle"`
	Checkpoints             []CheckpointRecord    `json:"checkpoints"`
	Approvals               []RuntimeApproval     `json:"approvals"`
	BudgetConsumed          BudgetUsage           `json:"budget_consumed"`
	VerificationObligations plan.VerificationPlan `json:"verification_obligations"`
	Contradictions          []string              `json:"contradictions,omitempty"`
	Limitations             []string              `json:"limitations,omitempty"`
	CompletedAt             time.Time             `json:"completed_at"`
}
