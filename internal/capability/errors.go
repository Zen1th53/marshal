package capability

import "errors"

// ErrorCode is the stable machine-readable reason for a capability outcome.
type ErrorCode string

const (
	CodeInvalidScope    ErrorCode = "CAP_INVALID_SCOPE"
	CodeDenied          ErrorCode = "CAP_DENIED"
	CodeExpired         ErrorCode = "CAP_EXPIRED"
	CodeRevoked         ErrorCode = "CAP_REVOKED"
	CodeSubjectMismatch ErrorCode = "CAP_SUBJECT_MISMATCH"
	CodeTaskMismatch    ErrorCode = "CAP_TASK_MISMATCH"
)

var (
	ErrInvalidScope    = &Error{Code: CodeInvalidScope, message: "capability scope is invalid"}
	ErrDenied          = &Error{Code: CodeDenied, message: "capability denied"}
	ErrExpired         = &Error{Code: CodeExpired, message: "capability grant expired"}
	ErrRevoked         = &Error{Code: CodeRevoked, message: "capability grant revoked"}
	ErrSubjectMismatch = &Error{Code: CodeSubjectMismatch, message: "capability subject mismatch"}
	ErrTaskMismatch    = &Error{Code: CodeTaskMismatch, message: "capability task mismatch"}
)

// Error is a safe, structured capability error. Detail supplied by callers is
// intentionally not retained, preventing untrusted or sensitive input from
// leaking through error messages.
type Error struct {
	Code    ErrorCode
	message string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e != nil && other != nil && e.Code == other.Code
}

// NewError returns the canonical safe error for code. Unknown codes fail
// closed as invalid scope rather than creating an unclassified outcome.
func NewError(code ErrorCode, _ string) *Error {
	switch code {
	case CodeInvalidScope:
		return ErrInvalidScope
	case CodeDenied:
		return ErrDenied
	case CodeExpired:
		return ErrExpired
	case CodeRevoked:
		return ErrRevoked
	case CodeSubjectMismatch:
		return ErrSubjectMismatch
	case CodeTaskMismatch:
		return ErrTaskMismatch
	default:
		return ErrInvalidScope
	}
}

func (e *Error) Unwrap() error { return errors.New(string(e.Code)) }
