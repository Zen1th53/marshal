package events

import "errors"

// Code is the stable machine-readable T43 failure vocabulary.
type Code string

const (
	CodeInvalidType      Code = "EVENT_TYPE_INVALID"
	CodeSecretField      Code = "EVENT_SECRET_FIELD"
	CodeStoreFailed      Code = "EVENT_STORE_FAILED"
	CodeSequenceConflict Code = "EVENT_SEQUENCE_CONFLICT"
	CodeInvalidEvent     Code = "EVENT_INVALID"
)

// Error exposes only a stable safe message while retaining a wrapped cause
// for errors.Is/As diagnostics.
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
	ErrInvalidType      = &Error{Code: CodeInvalidType, Message: "event type is invalid"}
	ErrSecretField      = &Error{Code: CodeSecretField, Message: "event contains forbidden sensitive data"}
	ErrStoreFailed      = &Error{Code: CodeStoreFailed, Message: "event store operation failed"}
	ErrSequenceConflict = &Error{Code: CodeSequenceConflict, Message: "event sequence conflict"}
	ErrInvalidEvent     = &Error{Code: CodeInvalidEvent, Message: "event is invalid"}
)

func NewError(code Code, cause error) error {
	return &Error{Code: code, Message: safeMessage(code), Err: cause}
}

func ReasonCode(err error) Code {
	var eventErr *Error
	if errors.As(err, &eventErr) {
		return eventErr.Code
	}
	return ""
}

func safeMessage(code Code) string {
	switch code {
	case CodeInvalidType:
		return ErrInvalidType.Message
	case CodeSecretField:
		return ErrSecretField.Message
	case CodeStoreFailed:
		return ErrStoreFailed.Message
	case CodeSequenceConflict:
		return ErrSequenceConflict.Message
	case CodeInvalidEvent:
		return ErrInvalidEvent.Message
	default:
		return "event operation failed"
	}
}
