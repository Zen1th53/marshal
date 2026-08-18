package budget

type Code string

const (
	CodeTooLarge             Code = "CTXBUDGET_TOO_LARGE"
	CodeMandatoryOverflow    Code = "CTXBUDGET_MANDATORY_OVERFLOW"
	CodeEstimatorUnavailable Code = "CTXBUDGET_ESTIMATOR_UNAVAILABLE"
)

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
	ErrTooLarge             = &Error{Code: CodeTooLarge, Message: "context size exceeds maximum budget limit"}
	ErrMandatoryOverflow    = &Error{Code: CodeMandatoryOverflow, Message: "mandatory context sections exceed token budget"}
	ErrEstimatorUnavailable = &Error{Code: CodeEstimatorUnavailable, Message: "token estimator service unavailable"}
)

type Budget struct {
	MaxTokens     int `json:"max_tokens"`
	ReserveTokens int `json:"reserve_tokens"`
	Threshold     int `json:"threshold"`
}

type SectionPriority struct {
	Kind      string `json:"kind"`
	Priority  int    `json:"priority"`
	MinTokens int    `json:"min_tokens"`
	Mandatory bool   `json:"mandatory"`
}

type Decision struct {
	Action          string   `json:"action"`
	Dropped         []string `json:"dropped,omitempty"`
	Compacted       []string `json:"compacted,omitempty"`
	EstimatedTokens int      `json:"estimated_tokens"`
}
