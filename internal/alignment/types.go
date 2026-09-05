package alignment

import (
	"errors"
	"time"
)

var (
	ErrAlignmentViolation     = errors.New("alignment guard violation: proposed changes violate Goal scope or safety invariants")
	ErrBlastRadiusExceeded    = errors.New("blast radius exceeded: changes exceed allowed file/path limits for task scope")
	ErrDeletionAsSatisfaction = errors.New("deletion-as-satisfaction detected: cannot resolve failures by removing tests, targets, or error checks")
	ErrValidationRemoval      = errors.New("validation removal detected: disabling or bypassing security/validation checks is forbidden")
	ErrScopeViolation         = errors.New("scope violation: modified files fall outside allowed Goal scope")
	ErrForbiddenOperation     = errors.New("forbidden operation: destructive or unsafe system operation detected")
	ErrGoalDrift              = errors.New("goal version drift: task is executing against an obsolete Goal revision")
)

type CheckType string

const (
	CheckScopeLock              CheckType = "SCOPE_LOCK"
	CheckBlastRadius            CheckType = "BLAST_RADIUS"
	CheckForbiddenOps           CheckType = "FORBIDDEN_OPERATIONS"
	CheckDeletionAsSatisfaction CheckType = "DELETION_AS_SATISFACTION"
	CheckValidationRemoval      CheckType = "VALIDATION_REMOVAL"
	CheckOutcomeMismatch        CheckType = "OUTCOME_MISMATCH"
	CheckGoalDrift              CheckType = "GOAL_DRIFT"
)

type Violation struct {
	Type             CheckType `json:"type"`
	Severity         string    `json:"severity"` // "BLOCKING", "WARNING"
	Path             string    `json:"path,omitempty"`
	Message          string    `json:"message"`
	RequiresApproval bool      `json:"requires_approval"`
}

type Result struct {
	Passed          bool        `json:"passed"`
	Violations      []Violation `json:"violations,omitempty"`
	PredictedFiles  []string    `json:"predicted_files,omitempty"`
	ObservedFiles   []string    `json:"observed_files,omitempty"`
	PredictedRadius int         `json:"predicted_radius"`
	ObservedRadius  int         `json:"observed_radius"`
	EvaluatedAt     time.Time   `json:"evaluated_at"`
}
