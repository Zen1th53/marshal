package reinjection

import (
	"errors"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrConstraintWeakeningForbidden = errors.New("constraint weakening forbidden: agent cannot delete or weaken active hard constraints")
	ErrConstraintTampered           = errors.New("constraint integrity failure: handoff constraint digest does not match canonical Goal")
	ErrStaleGoalRevision            = errors.New("stale Goal revision: handoff references obsolete Goal constraints")
	ErrUnauthorizedSecretAccess     = errors.New("unauthorized constraint access: principal lacks capability to view secret constraint")
	ErrMissingHardConstraint        = errors.New("missing hard constraint: mandatory operator constraint omitted")
)

// CompiledConstraints represents the immutable, bounded representation of
// Goal constraints formatted for context re-injection.
type CompiledConstraints struct {
	GoalID            string             `json:"goal_id"`
	Revision          int64              `json:"revision"`
	Digest            string             `json:"digest"`
	ActiveConstraints []model.Constraint `json:"active_constraints"`
	DoNotDo           []string           `json:"do_not_do"`
	CompiledXML       string             `json:"compiled_xml"`
	CompiledAt        time.Time          `json:"compiled_at"`
}
