package scheduler

type Code string

const (
	CodeNoEligibleAgent    Code = "SCHED_NO_ELIGIBLE_AGENT"
	CodeLeaseConflict      Code = "SCHED_LEASE_CONFLICT"
	CodeTaskNotReady       Code = "SCHED_TASK_NOT_READY"
	CodeStaleWorker        Code = "SCHED_STALE_WORKER"
	CodeCapabilityMissing Code = "SCHED_CAPABILITY_MISSING"
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
	ErrNoEligibleAgent    = &Error{Code: CodeNoEligibleAgent, Message: "no eligible agent available for task criteria"}
	ErrLeaseConflict      = &Error{Code: CodeLeaseConflict, Message: "scheduler lease renewal conflict"}
	ErrTaskNotReady       = &Error{Code: CodeTaskNotReady, Message: "task is not ready for scheduling"}
	ErrStaleWorker        = &Error{Code: CodeStaleWorker, Message: "worker lease is stale or expired"}
	ErrCapabilityMissing = &Error{Code: CodeCapabilityMissing, Message: "required worker capability missing"}
)

type Candidate struct {
	AgentID            string   `json:"agent_id"`
	Provider           string   `json:"provider"`
	Capabilities       []string `json:"capabilities,omitempty"`
	Load               float64  `json:"load"`
	ContextUtilization float64  `json:"context_utilization"`
	SuccessRate        float64  `json:"success_rate"`
	EstimatedCost      float64  `json:"estimated_cost"`
}

type Task struct {
	TaskID               string   `json:"task_id"`
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
}

type Assignment struct {
	TaskID  string   `json:"task_id"`
	AgentID string   `json:"agent_id"`
	LeaseID string   `json:"lease_id"`
	Score   float64  `json:"score"`
	Reasons []string `json:"reasons,omitempty"`
}
