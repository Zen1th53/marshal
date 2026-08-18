package trustscore

type Code string

const (
	CodeStale Code = "TRUSTSCORE_STALE"
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
	ErrStale = &Error{Code: CodeStale, Message: "trust score evidence input is stale"}
)

type Component struct {
	Name        string   `json:"name"`
	Score       float64  `json:"score"`
	Weight      float64  `json:"weight"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	HardFail    bool     `json:"hard_fail"`
	Ceiling     float64  `json:"ceiling"`
}

type Result struct {
	Overall      float64              `json:"overall"`
	Components   map[string]Component `json:"components,omitempty"`
	PolicyDigest string               `json:"policy_digest"`
	ChangeDigest string               `json:"change_digest"`
	Eligible     bool                 `json:"eligible"`
	Reasons      []string             `json:"reasons,omitempty"`
}
