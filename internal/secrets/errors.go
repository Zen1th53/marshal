package secrets

import "errors"

type ErrorCode string

const (
	CodeDenied          ErrorCode = "SECRET_DENIED"
	CodeNotFound        ErrorCode = "SECRET_NOT_FOUND"
	CodeLeaseExpired    ErrorCode = "SECRET_LEASE_EXPIRED"
	CodePurposeMismatch ErrorCode = "SECRET_PURPOSE_MISMATCH"
	CodeProviderFailed  ErrorCode = "SECRET_PROVIDER_FAILED"
)

type Error struct {
	Code ErrorCode
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return safeMessage(e.Code)
}

func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e != nil && other != nil && e.Code == other.Code
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return errors.New(string(e.Code))
}

var (
	ErrDenied          = &Error{Code: CodeDenied}
	ErrNotFound        = &Error{Code: CodeNotFound}
	ErrLeaseExpired    = &Error{Code: CodeLeaseExpired}
	ErrPurposeMismatch = &Error{Code: CodePurposeMismatch}
	ErrProviderFailed  = &Error{Code: CodeProviderFailed}
)

func NewError(code ErrorCode, _ error) *Error {
	switch code {
	case CodeDenied:
		return ErrDenied
	case CodeNotFound:
		return ErrNotFound
	case CodeLeaseExpired:
		return ErrLeaseExpired
	case CodePurposeMismatch:
		return ErrPurposeMismatch
	case CodeProviderFailed:
		return ErrProviderFailed
	default:
		return ErrDenied
	}
}

func safeMessage(code ErrorCode) string {
	switch code {
	case CodeDenied:
		return "secret access denied"
	case CodeNotFound:
		return "secret reference not found"
	case CodeLeaseExpired:
		return "secret lease expired"
	case CodePurposeMismatch:
		return "secret lease purpose mismatch"
	case CodeProviderFailed:
		return "secret provider failed"
	default:
		return "secret operation failed"
	}
}
