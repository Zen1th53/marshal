package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInterpretationInvalid = errors.New("invalid interpretation")
)

// Interpretation represents an agent's independent, unanchored reading of intent.
type Interpretation struct {
	ID               string           `json:"id"`
	GoalID           string           `json:"goal_id"`
	GoalRevision     int64            `json:"goal_revision"`
	SessionID        string           `json:"session_id"`
	Author           AuthorProvenance `json:"author"`
	DesiredOutcome   string           `json:"desired_outcome"`
	ExpectedArtifact string           `json:"expected_artifact"`
	Scope            []string         `json:"scope"`
	IdentifiedRisks  []string         `json:"identified_risks"`
	Constraints      []Constraint     `json:"constraints"`
	Assumptions      []Assumption     `json:"assumptions"`
	SuccessCriteria  []string         `json:"success_criteria"`
	IsDestructive    bool             `json:"is_destructive"`
	Ambiguities      []string         `json:"ambiguities"`
	SubmittedAt      time.Time        `json:"submitted_at"`
}

func (i Interpretation) Validate() error {
	if strings.TrimSpace(i.ID) == "" {
		return fmt.Errorf("%w: interpretation ID is required", ErrInterpretationInvalid)
	}
	if strings.TrimSpace(i.GoalID) == "" {
		return fmt.Errorf("%w: goal ID is required", ErrInterpretationInvalid)
	}
	if i.GoalRevision < 0 {
		return fmt.Errorf("%w: goal revision cannot be negative", ErrInterpretationInvalid)
	}
	if strings.TrimSpace(i.SessionID) == "" {
		return fmt.Errorf("%w: session ID is required", ErrInterpretationInvalid)
	}
	if strings.TrimSpace(i.Author.AgentID) == "" {
		return fmt.Errorf("%w: author agent ID is required", ErrInterpretationInvalid)
	}
	if strings.TrimSpace(i.DesiredOutcome) == "" {
		return fmt.Errorf("%w: desired outcome is required", ErrInterpretationInvalid)
	}
	if i.SubmittedAt.IsZero() {
		return fmt.Errorf("%w: submitted_at timestamp is required", ErrInterpretationInvalid)
	}
	return nil
}

// InterpretationRequirement specifies the diversity & count constraints for blind interpretation.
type InterpretationRequirement struct {
	MinInterpreters             int    `json:"min_interpreters"`
	RequireHeterogeneousHarness bool   `json:"require_heterogeneous_harness"`
	RequireDifferentModels      bool   `json:"require_different_models"`
	Reason                      string `json:"reason"`
}

type DivergenceKind string

const (
	DivergenceScope       DivergenceKind = "SCOPE_MISMATCH"
	DivergenceOutcome     DivergenceKind = "OUTCOME_MISMATCH"
	DivergenceConstraint  DivergenceKind = "CONSTRAINT_CONFLICT"
	DivergenceDestructive DivergenceKind = "DESTRUCTIVE_ACTION_DISAGREEMENT"
	DivergenceAssumptions DivergenceKind = "CONTRADICTORY_ASSUMPTIONS"
)

// Divergence details a material contradiction or ambiguity between independent interpretations.
type Divergence struct {
	Kind        DivergenceKind `json:"kind"`
	Field       string         `json:"field"`
	Description string         `json:"description"`
	Question    string         `json:"question"`
	Impact      string         `json:"impact"`
	Options     []string       `json:"options"`
}

// InterpretationResolution records the outcome of comparing blind interpretations.
// UX rule: No artificial percentages. State is strictly READY or NEEDS_INPUT.
type InterpretationResolution struct {
	ID                 string               `json:"id"`
	SessionID          string               `json:"session_id"`
	GoalID             string               `json:"goal_id"`
	GoalRevision       int64                `json:"goal_revision"`
	State              UnderstandingState   `json:"state"` // READY or NEEDS_INPUT
	RequiredCount      int                  `json:"required_count"`
	CollectedCount     int                  `json:"collected_count"`
	Divergences        []Divergence         `json:"divergences,omitempty"`
	ConcreteQuestions  []UnresolvedDecision `json:"concrete_questions,omitempty"`
	ConsensusConfirmed bool                 `json:"consensus_confirmed"`
	Message            string               `json:"message"`
	ResolvedAt         time.Time            `json:"resolved_at"`
}

func (r InterpretationResolution) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("%w: resolution ID is required", ErrInterpretationInvalid)
	}
	if strings.TrimSpace(r.GoalID) == "" {
		return fmt.Errorf("%w: goal ID is required", ErrInterpretationInvalid)
	}
	if r.State != GoalReady && r.State != GoalNeedsInput {
		return fmt.Errorf("%w: state must be READY or NEEDS_INPUT, got %q", ErrInterpretationInvalid, r.State)
	}
	if r.ResolvedAt.IsZero() {
		return fmt.Errorf("%w: resolved_at is required", ErrInterpretationInvalid)
	}
	return nil
}
