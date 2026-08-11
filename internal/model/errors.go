package model

import "errors"

var (
	ErrConflict         = errors.New("conflict")
	ErrInvalid          = errors.New("invalid input")
	ErrNotFound         = errors.New("not found")
	ErrUnavailable      = errors.New("unavailable")
	ErrPolicyDenied     = errors.New("policy denied")
	ErrApprovalRequired = errors.New("approval required")
	ErrSecretMaterial   = errors.New("secret material")
	ErrDirtyWorktree    = errors.New("dirty worktree")
)

type CodeError struct {
	Code    string
	Message string
	Err     error
}

func (e *CodeError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func (e *CodeError) Unwrap() error {
	return e.Err
}
