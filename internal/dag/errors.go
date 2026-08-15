package dag

import "errors"

// Code is the stable, machine-readable T29 failure vocabulary.
type Code string

const (
	CodeCycle                    Code = "DAG_CYCLE"
	CodeNodeNotFound             Code = "DAG_NODE_NOT_FOUND"
	CodeDuplicateEdge            Code = "DAG_DUPLICATE_EDGE"
	CodeInvalidCondition         Code = "DAG_INVALID_CONDITION"
	CodeInvalidNode              Code = "DAG_NODE_INVALID"
	CodeInvalidRequest           Code = "DAG_REQUEST_INVALID"
	CodeAuthorizationDenied      Code = "DAG_AUTHZ_DENIED"
	CodeAuthorizationStale       Code = "DAG_AUTHZ_STALE"
	CodeAuthorizationUnavailable Code = "DAG_AUTHZ_UNAVAILABLE"
	CodeEventUnavailable         Code = "DAG_EVENT_UNAVAILABLE"
)

// Error is safe to expose: Message is selected only from the stable code and
// never includes caller-controlled data or the wrapped cause text.
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
	ErrCycle                    = &Error{Code: CodeCycle, Message: "dag cycle rejected"}
	ErrNodeNotFound             = &Error{Code: CodeNodeNotFound, Message: "dag node not found"}
	ErrDuplicateEdge            = &Error{Code: CodeDuplicateEdge, Message: "dag edge already exists"}
	ErrInvalidCondition         = &Error{Code: CodeInvalidCondition, Message: "dag edge condition is invalid"}
	ErrInvalidNode              = &Error{Code: CodeInvalidNode, Message: "dag node is invalid"}
	ErrInvalidRequest           = &Error{Code: CodeInvalidRequest, Message: "dag request is invalid"}
	ErrAuthorizationDenied      = &Error{Code: CodeAuthorizationDenied, Message: "dag authorization denied"}
	ErrAuthorizationStale       = &Error{Code: CodeAuthorizationStale, Message: "dag authorization is stale"}
	ErrAuthorizationUnavailable = &Error{Code: CodeAuthorizationUnavailable, Message: "dag authorization is unavailable"}
	ErrEventUnavailable         = &Error{Code: CodeEventUnavailable, Message: "dag event sink is unavailable"}
)

func NewError(code Code, cause error) error {
	return &Error{Code: code, Message: safeMessage(code), Err: cause}
}

func ReasonCode(err error) Code {
	var dagErr *Error
	if errors.As(err, &dagErr) {
		return dagErr.Code
	}
	return ""
}

func safeMessage(code Code) string {
	switch code {
	case CodeCycle:
		return ErrCycle.Message
	case CodeNodeNotFound:
		return ErrNodeNotFound.Message
	case CodeDuplicateEdge:
		return ErrDuplicateEdge.Message
	case CodeInvalidCondition:
		return ErrInvalidCondition.Message
	case CodeInvalidNode:
		return ErrInvalidNode.Message
	case CodeInvalidRequest:
		return ErrInvalidRequest.Message
	case CodeAuthorizationDenied:
		return ErrAuthorizationDenied.Message
	case CodeAuthorizationStale:
		return ErrAuthorizationStale.Message
	case CodeAuthorizationUnavailable:
		return ErrAuthorizationUnavailable.Message
	case CodeEventUnavailable:
		return ErrEventUnavailable.Message
	default:
		return "dag operation failed"
	}
}
