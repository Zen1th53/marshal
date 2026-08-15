package policy

import "errors"

// ErrorCode is stable machine-readable policy failure vocabulary.
type ErrorCode string

const (
	CodeParseError        ErrorCode = "POLICY_PARSE_ERROR"
	CodeUnknownField      ErrorCode = "POLICY_UNKNOWN_FIELD"
	CodeUnknownAction     ErrorCode = "POLICY_UNKNOWN_ACTION"
	CodeConflict          ErrorCode = "POLICY_CONFLICT"
	CodeDeny              ErrorCode = "POLICY_DENY"
	CodeInvalidDecision   ErrorCode = CodeParseError
	CodeInvalidObligation ErrorCode = CodeParseError
	CodeStaleBinding      ErrorCode = CodeConflict
)

// Error is safe to expose: its message is selected only from the code and
// never includes untrusted policy text or backend error contents.
type Error struct {
	Code ErrorCode
	Err  error
}

func (e *Error) Error() string { return string(e.Code) }
func (e *Error) Unwrap() error { return e.Err }
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e.Code == other.Code
}

func NewError(code ErrorCode, cause error) error { return &Error{Code: code, Err: cause} }

func ReasonCode(err error) ErrorCode {
	var policyErr *Error
	if errors.As(err, &policyErr) {
		return policyErr.Code
	}
	return ""
}

var (
	ErrParseError        = &Error{Code: CodeParseError}
	ErrUnknownField      = &Error{Code: CodeUnknownField}
	ErrUnknownAction     = &Error{Code: CodeUnknownAction}
	ErrConflict          = &Error{Code: CodeConflict}
	ErrDeny              = &Error{Code: CodeDeny}
	ErrInvalidDecision   = &Error{Code: CodeInvalidDecision}
	ErrInvalidObligation = &Error{Code: CodeInvalidObligation}
	ErrStaleBinding      = &Error{Code: CodeStaleBinding}
)
