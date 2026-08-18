package tournament

type Code string

const (
	CodeBudgetExceeded   Code = "TOURNAMENT_BUDGET_EXCEEDED"
	CodeNoValidCandidate Code = "TOURNAMENT_NO_VALID_CANDIDATE"
	CodeCellFailed       Code = "TOURNAMENT_CELL_FAILED"
	CodeScoreInvalid     Code = "TOURNAMENT_SCORE_INVALID"
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
	ErrBudgetExceeded   = &Error{Code: CodeBudgetExceeded, Message: "tournament compute budget exceeded"}
	ErrNoValidCandidate = &Error{Code: CodeNoValidCandidate, Message: "no candidate met minimum hard bounds"}
	ErrCellFailed       = &Error{Code: CodeCellFailed, Message: "tournament evaluation cell execution failed"}
	ErrScoreInvalid     = &Error{Code: CodeScoreInvalid, Message: "invalid candidate score dimension"}
)

type CandidateRun struct {
	ID       string             `json:"id"`
	TaskID   string             `json:"task_id"`
	AgentID  string             `json:"agent_id"`
	CellID   string             `json:"cell_id"`
	ChangeID string             `json:"change_id"`
	Metrics  map[string]float64 `json:"metrics,omitempty"`
}

type Dimension struct {
	Name           string  `json:"name"`
	Weight         float64 `json:"weight"`
	HigherIsBetter bool    `json:"higher_is_better"`
	HardMinimum    float64 `json:"hard_minimum"`
}

type Result struct {
	WinnerID    string   `json:"winner_id"`
	Ranking     []string `json:"ranking,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}
