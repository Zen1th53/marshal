package risk

import "errors"

// ErrorCode is the stable machine-readable risk assessment vocabulary.
type ErrorCode string

const (
	CodeDescriptorInvalid        ErrorCode = "RISK_DESCRIPTOR_INVALID"
	CodeUnknownMutation          ErrorCode = "RISK_UNKNOWN_MUTATION"
	CodeDowngradeForbidden       ErrorCode = "RISK_DOWNGRADE_FORBIDDEN"
	CodeAuthorizationUnavailable ErrorCode = "RISK_AUTHORIZATION_UNAVAILABLE"
	CodeAuthorizationDenied      ErrorCode = "RISK_AUTHORIZATION_DENIED"
)

var (
	ErrDescriptorInvalid        = &Error{Code: CodeDescriptorInvalid, message: "risk descriptor is invalid"}
	ErrUnknownMutation          = &Error{Code: CodeUnknownMutation, message: "risk assessment rejected an unknown mutating action"}
	ErrDowngradeForbidden       = &Error{Code: CodeDowngradeForbidden, message: "risk level downgrade is forbidden"}
	ErrAuthorizationUnavailable = &Error{Code: CodeAuthorizationUnavailable, message: "risk authorization is unavailable"}
	ErrAuthorizationDenied      = &Error{Code: CodeAuthorizationDenied, message: "risk authorization denied"}
)

// Error contains no caller-controlled detail so it is safe for public surfaces.
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

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return errors.New(string(e.Code))
}

func NewError(code ErrorCode, _ string) *Error {
	switch code {
	case CodeDescriptorInvalid:
		return ErrDescriptorInvalid
	case CodeUnknownMutation:
		return ErrUnknownMutation
	case CodeDowngradeForbidden:
		return ErrDowngradeForbidden
	case CodeAuthorizationUnavailable:
		return ErrAuthorizationUnavailable
	case CodeAuthorizationDenied:
		return ErrAuthorizationDenied
	default:
		return ErrDescriptorInvalid
	}
}
