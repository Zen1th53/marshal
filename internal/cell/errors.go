package cell

type ErrorCode string

const (
	CodeBackendUnavailable ErrorCode = "CELL_BACKEND_UNAVAILABLE"
	CodePrepareFailed      ErrorCode = "CELL_PREPARE_FAILED"
	CodeScopeEscape        ErrorCode = "CELL_SCOPE_ESCAPE"
	CodeNotReady           ErrorCode = "CELL_NOT_READY"
	CodeDestroyed          ErrorCode = "CELL_DESTROYED"
	CodeCleanupFailed      ErrorCode = "CELL_CLEANUP_FAILED"
)

type Error struct {
	Code ErrorCode
}

func (e *Error) Error() string { return string(e.Code) }

func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e.Code == other.Code
}

var (
	ErrBackendUnavailable = &Error{Code: CodeBackendUnavailable}
	ErrPrepareFailed      = &Error{Code: CodePrepareFailed}
	ErrScopeEscape        = &Error{Code: CodeScopeEscape}
	ErrNotReady           = &Error{Code: CodeNotReady}
	ErrDestroyed          = &Error{Code: CodeDestroyed}
	ErrCleanupFailed      = &Error{Code: CodeCleanupFailed}
)

var _ error = ErrBackendUnavailable
