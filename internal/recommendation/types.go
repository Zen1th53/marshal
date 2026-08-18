package recommendation

type Code string

const (
	CodeLowConfidence    Code = "OPTIMIZER_LOW_CONFIDENCE"
	CodeHardFloor        Code = "OPTIMIZER_HARD_FLOOR"
	CodeApprovalRequired Code = "OPTIMIZER_APPROVAL_REQUIRED"
	CodeConfigConflict   Code = "OPTIMIZER_CONFIG_CONFLICT"
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
	ErrLowConfidence    = &Error{Code: CodeLowConfidence, Message: "recommendation confidence below minimum threshold"}
	ErrHardFloor        = &Error{Code: CodeHardFloor, Message: "proposed change violates safety hard floor"}
	ErrApprovalRequired = &Error{Code: CodeApprovalRequired, Message: "human approval required before applying recommendation"}
	ErrConfigConflict   = &Error{Code: CodeConfigConflict, Message: "recommendation conflicts with existing system configuration"}
)

type Recommendation struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	Target         string   `json:"target"`
	ProposedChange string   `json:"proposed_change"`
	Rationale      string   `json:"rationale"`
	EvidenceIDs    []string `json:"evidence_ids,omitempty"`
	Confidence     float64  `json:"confidence"`
	Status         string   `json:"status"`
}
