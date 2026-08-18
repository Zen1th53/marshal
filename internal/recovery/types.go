package recovery

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

type Plan struct {
	TaskID       string `json:"task_id"`
	CheckpointID string `json:"checkpoint_id"`
	RetryCount   int    `json:"retry_count"`
	MaxRetries   int    `json:"max_retries"`
	Action       string `json:"action"`
}
