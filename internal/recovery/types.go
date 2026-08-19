package recovery

import "time"

type Code string

const (
	CodeRetryExhausted    Code = "RECOVERY_RETRY_EXHAUSTED"
	CodeNoValidCheckpoint Code = "RECOVERY_NO_VALID_CHECKPOINT"
	CodeConcurrentOwner   Code = "RECOVERY_CONCURRENT_OWNER"
	CodePolicyBlocked     Code = "RECOVERY_POLICY_BLOCKED"
)

type Error struct {
	Code    Code
	Message string
	Err     error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e.Code == other.Code
}

var (
	ErrRetryExhausted    = &Error{Code: CodeRetryExhausted, Message: "recovery retry count exhausted"}
	ErrNoValidCheckpoint = &Error{Code: CodeNoValidCheckpoint, Message: "no valid checkpoint found for recovery"}
	ErrConcurrentOwner   = &Error{Code: CodeConcurrentOwner, Message: "concurrent owner active during recovery attempt"}
	ErrPolicyBlocked     = &Error{Code: CodePolicyBlocked, Message: "recovery policy blocked auto recovery"}
)

type FailureType string

const (
	FailureWorkerCrash  FailureType = "worker_crash"
	FailureRuntimeCrash FailureType = "runtime_crash"
	FailureStaleLease   FailureType = "stale_lease"
	FailureTimeout      FailureType = "timeout"
	FailureUnknown      FailureType = "unknown"
)

type CheckpointState string

const (
	CheckpointValid    CheckpointState = "valid"
	CheckpointCorrupt  CheckpointState = "corrupt"
	CheckpointPoisoned CheckpointState = "poisoned"
)

type Checkpoint struct {
	ID         string          `json:"id"`
	TaskID     string          `json:"task_id"`
	BaseCommit string          `json:"base_commit"`
	HeadCommit string          `json:"head_commit"`
	Checksum   string          `json:"checksum"`
	State      CheckpointState `json:"state"`
	CreatedAt  time.Time       `json:"created_at"`
}

type RecoveryRequest struct {
	TaskID           string      `json:"task_id"`
	Checkpoint       *Checkpoint `json:"checkpoint,omitempty"`
	Failure          FailureType `json:"failure"`
	CurrentRetries   int         `json:"current_retries"`
	MaxRetries       int         `json:"max_retries"`
	ActiveLeaseOwner string      `json:"active_lease_owner,omitempty"`
	ForceRestart     bool        `json:"force_restart,omitempty"`
}

type Action string

const (
	ActionResumeFromCheckpoint Action = "RESUME_FROM_CHECKPOINT"
	ActionRestartFromBase      Action = "RESTART_FROM_BASE"
	ActionFailExhausted        Action = "FAIL_RETRY_EXHAUSTED"
	ActionBlockConcurrent      Action = "BLOCK_CONCURRENT_OWNER"
)

type Plan struct {
	TaskID         string   `json:"task_id"`
	CheckpointID   string   `json:"checkpoint_id,omitempty"`
	RetryCount     int      `json:"retry_count"`
	MaxRetries     int      `json:"max_retries"`
	Action         Action   `json:"action"`
	BackoffSeconds int      `json:"backoff_seconds"`
	Reasons        []string `json:"reasons,omitempty"`
}
