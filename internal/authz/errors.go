package authz

import "errors"

type ErrorCode string

const (
	CodeAllowed          ErrorCode = "AUTHZ_ALLOWED"
	CodeUnknownRole      ErrorCode = "AUTHZ_UNKNOWN_ROLE"
	CodeUnknownAuthority ErrorCode = "AUTHZ_UNKNOWN_AUTHORITY"
	CodeDenied           ErrorCode = "AUTHZ_DENIED"
	CodeSelfApproval     ErrorCode = "AUTHZ_SELF_APPROVAL"
	CodeRoleInvalid      ErrorCode = "AUTHZ_ROLE_INVALID"
)

var (
	ErrUnknownRole      = &Error{Code: CodeUnknownRole, message: "authorization role is unknown"}
	ErrUnknownAuthority = &Error{Code: CodeUnknownAuthority, message: "authorization authority is unknown"}
	ErrDenied           = &Error{Code: CodeDenied, message: "authorization denied"}
	ErrSelfApproval     = &Error{Code: CodeSelfApproval, message: "authorization self-approval is denied"}
	ErrRoleInvalid      = &Error{Code: CodeRoleInvalid, message: "authorization role is invalid"}
)

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

func codeOf(err error) ErrorCode {
	var coded *Error
	if errors.As(err, &coded) && coded != nil {
		return coded.Code
	}
	return CodeRoleInvalid
}
