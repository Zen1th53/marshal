package plan

import "errors"

// Sentinel errors for plan persistence.
//
// These live here rather than in the store because they describe planning
// outcomes rather than storage faults: a caller handling a conflict is making
// a decision about concurrent intentions, not about SQLite.
var (
	// ErrPlanNotFound means no such plan or version is stored.
	ErrPlanNotFound = errors.New("execution plan not found")
	// ErrPlanConflict means the caller's expected version was not the stored
	// one. Someone else wrote in between. It is deliberately not resolved
	// automatically: MARSHAL cannot know which of two concurrent intentions
	// was meant to win, and choosing would silently discard the other.
	ErrPlanConflict = errors.New("execution plan version conflict")
)

// ErrPlanInvalid is declared in validation.go: a plan that cannot be stored
// and a plan that cannot be built are the same fault seen from two sides, and
// splitting them would make callers match on two errors for one condition.
