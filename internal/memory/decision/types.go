package decision

import "time"

type Status string

const (
	StatusProposed   Status = "PROPOSED"
	StatusAccepted   Status = "ACCEPTED"
	StatusRejected   Status = "REJECTED"
	StatusSuperseded Status = "SUPERSEDED"
	StatusDeprecated Status = "DEPRECATED"
)

func (s Status) Valid() bool {
	switch s {
	case StatusProposed, StatusAccepted, StatusRejected, StatusSuperseded, StatusDeprecated:
		return true
	default:
		return false
	}
}

type Code string

const (
	CodeInvalidStatus       Code = "DECISION_INVALID_STATUS"
	CodeAuthorityRequired   Code = "DECISION_AUTHORITY_REQUIRED"
	CodeAlreadyFinal        Code = "DECISION_ALREADY_FINAL"
	CodeSupersessionInvalid Code = "DECISION_SUPERSESSION_INVALID"
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
	ErrInvalidStatus       = &Error{Code: CodeInvalidStatus, Message: "decision record status is invalid"}
	ErrAuthorityRequired   = &Error{Code: CodeAuthorityRequired, Message: "authority approval required to finalize decision"}
	ErrAlreadyFinal        = &Error{Code: CodeAlreadyFinal, Message: "decision record is already final"}
	ErrSupersessionInvalid = &Error{Code: CodeSupersessionInvalid, Message: "supersession reference is invalid"}
)

type DecisionRecord struct {
	ID           string    `json:"id"`
	TaskID       string    `json:"task_id"`
	AgentID      string    `json:"agent_id"`
	Title        string    `json:"title"`
	Context      string    `json:"context"`
	Decision     string    `json:"decision"`
	Consequences string    `json:"consequences,omitempty"`
	Status       Status    `json:"status"`
	AuthorityID  string    `json:"authority_id,omitempty"`
	Supersedes   string    `json:"supersedes,omitempty"`
	SupersededBy string    `json:"superseded_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
