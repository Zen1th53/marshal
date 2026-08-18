package evolution

type Code string

const (
	CodeBudgetExceeded   Code = "EVOLVE_BUDGET_EXCEEDED"
	CodeMutationInvalid  Code = "EVOLVE_MUTATION_INVALID"
	CodeArchiveCorrupt   Code = "EVOLVE_ARCHIVE_CORRUPT"
	CodeProtectedTarget  Code = "EVOLVE_PROTECTED_TARGET"
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
	ErrBudgetExceeded  = &Error{Code: CodeBudgetExceeded, Message: "evolution generation compute budget exceeded"}
	ErrMutationInvalid = &Error{Code: CodeMutationInvalid, Message: "mutation operator generated invalid feature vector"}
	ErrArchiveCorrupt  = &Error{Code: CodeArchiveCorrupt, Message: "evolution archive data corrupt"}
	ErrProtectedTarget = &Error{Code: CodeProtectedTarget, Message: "mutation targeted protected core engine code"}
)

type Individual struct {
	ID         string             `json:"id"`
	ParentID   string             `json:"parent_id,omitempty"`
	ChangeID   string             `json:"change_id"`
	Generation int                `json:"generation"`
	Features   map[string]float64 `json:"features,omitempty"`
	Fitness    float64            `json:"fitness"`
}

type LabConfig struct {
	Population  int      `json:"population"`
	Generations int      `json:"generations"`
	MaxParallel int      `json:"max_parallel"`
	Dimensions  []string `json:"dimensions,omitempty"`
}

type LabResult struct {
	BestIndividual Individual `json:"best_individual"`
	GenerationsRun int        `json:"generations_run"`
	ArchiveIDs     []string   `json:"archive_ids,omitempty"`
}
