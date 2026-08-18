package compiler

import "time"

type Code string

const (
	CodeReadDenied          Code = "CTX_READ_DENIED"
	CodeBudgetUnsatisfiable Code = "CTX_BUDGET_UNSATISFIABLE"
	CodeSourceInvalid       Code = "CTX_SOURCE_INVALID"
	CodeSecretRejected      Code = "CTX_SECRET_REJECTED"
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
	ErrReadDenied          = &Error{Code: CodeReadDenied, Message: "context read permission denied"}
	ErrBudgetUnsatisfiable = &Error{Code: CodeBudgetUnsatisfiable, Message: "context size exceeds budget limit"}
	ErrSourceInvalid       = &Error{Code: CodeSourceInvalid, Message: "context source reference is invalid"}
	ErrSecretRejected      = &Error{Code: CodeSecretRejected, Message: "context text contains forbidden secret data"}
)

type CompiledContext struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id"`
	AgentID     string    `json:"agent_id"`
	MemoryIDs   []string  `json:"memory_ids,omitempty"`
	DecisionIDs []string  `json:"decision_ids,omitempty"`
	PromptText  string    `json:"prompt_text"`
	TokenCount  int       `json:"token_count"`
	BudgetLimit int       `json:"budget_limit"`
	CreatedAt   time.Time `json:"created_at"`
}
