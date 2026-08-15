package policytest

import "errors"

// ErrorCode is the closed machine-readable error vocabulary for policy tests.
type ErrorCode string

const (
	CodeCaseInvalid         ErrorCode = "POLICYTEST_CASE_INVALID"
	CodeExpectationMismatch ErrorCode = "POLICYTEST_EXPECTATION_MISMATCH"
	CodeRunInvalid          ErrorCode = "POLICYTEST_RUN_INVALID"
	CodeStateInvalid        ErrorCode = "POLICYTEST_STATE_INVALID"
	CodeIllegalTransition   ErrorCode = "POLICYTEST_ILLEGAL_TRANSITION"
	CodeStaleState          ErrorCode = "POLICYTEST_STALE_STATE"
)

// Error intentionally exposes only a stable code. Fixture text and evaluator
// details must never be copied into public contract-validation errors.
type Error struct {
	Code ErrorCode
}

func (e *Error) Error() string { return string(e.Code) }

func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e.Code == other.Code
}

func NewError(code ErrorCode) error { return &Error{Code: code} }

func ReasonCode(err error) ErrorCode {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}

var (
	ErrCaseInvalid         = &Error{Code: CodeCaseInvalid}
	ErrExpectationMismatch = &Error{Code: CodeExpectationMismatch}
	ErrStateInvalid        = &Error{Code: CodeStateInvalid}
	ErrIllegalTransition   = &Error{Code: CodeIllegalTransition}
	ErrStaleState          = &Error{Code: CodeStaleState}
)
