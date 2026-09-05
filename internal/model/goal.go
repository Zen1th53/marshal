package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrGoalNotFound         = errors.New("goal contract not found")
	ErrGoalConflict         = errors.New("goal contract revision conflict")
	ErrGoalHardConstraint   = errors.New("unauthorized removal or weakening of hard operator constraint")
	ErrGoalInvalid          = errors.New("invalid goal contract")
)

type UnderstandingState string

const (
	GoalReady      UnderstandingState = "READY"
	GoalNeedsInput UnderstandingState = "NEEDS_INPUT"
)

type Constraint struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Source string `json:"source"`  // e.g. "operator", "policy", "system"
	IsHard bool   `json:"is_hard"` // hard constraint vs guideline
	Scope  string `json:"scope"`   // paths or semantic scope affected
}

type Assumption struct {
	ID           string `json:"id"`
	Text         string `json:"text"`
	Risk         string `json:"risk"` // "low", "high"
	IsReversible bool   `json:"is_reversible"`
	CreatedBy    string `json:"created_by"`
}

type UnresolvedDecision struct {
	ID           string   `json:"id"`
	Question     string   `json:"question"`
	Impact       string   `json:"impact"`
	Options      []string `json:"options"`
	RequiresUser bool     `json:"requires_user"`
}

type GoalContract struct {
	ID                     string               `json:"id"`
	SessionID              string               `json:"session_id"`
	Revision               int64                `json:"revision"`
	DesiredOutcome         string               `json:"desired_outcome"`
	ExpectedArtifact       string               `json:"expected_artifact"`
	Scope                  []string             `json:"scope"`
	Constraints            []Constraint         `json:"constraints"`
	DoNotDo                []string             `json:"do_not_do"`
	SuccessCriteria        []string             `json:"success_criteria"`
	Risk                   Risk                 `json:"risk"`
	AuthoritySource        string               `json:"authority_source"`
	BudgetRef              string               `json:"budget_ref"`
	RequiredCriticalClaims []string             `json:"required_critical_claims"`
	UnderstandingState     UnderstandingState   `json:"understanding_state"`
	UnresolvedDecisions    []UnresolvedDecision `json:"unresolved_decisions"`
	Assumptions            []Assumption         `json:"assumptions"`
	RepoCommit             string               `json:"repo_commit"`
	CreatedAt              time.Time            `json:"created_at"`
	UpdatedAt              time.Time            `json:"updated_at"`
}

// EvaluateUnderstanding determines the user-visible understanding state:
// READY: enough intent to begin safely
// NEEDS_INPUT: an unresolved decision or high-risk assumption exists before continuation
func (g *GoalContract) EvaluateUnderstanding() UnderstandingState {
	if len(g.UnresolvedDecisions) > 0 {
		g.UnderstandingState = GoalNeedsInput
		return GoalNeedsInput
	}
	for _, a := range g.Assumptions {
		if strings.EqualFold(a.Risk, "high") || !a.IsReversible {
			g.UnderstandingState = GoalNeedsInput
			return GoalNeedsInput
		}
	}
	if strings.TrimSpace(g.DesiredOutcome) == "" {
		g.UnderstandingState = GoalNeedsInput
		return GoalNeedsInput
	}
	g.UnderstandingState = GoalReady
	return GoalReady
}

func ValidateGoal(g GoalContract) error {
	if strings.TrimSpace(g.ID) == "" {
		return fmt.Errorf("%w: goal ID is required", ErrGoalInvalid)
	}
	if strings.TrimSpace(g.SessionID) == "" {
		return fmt.Errorf("%w: session ID is required", ErrGoalInvalid)
	}
	if g.Revision < 1 {
		return fmt.Errorf("%w: revision must be >= 1", ErrGoalInvalid)
	}
	if strings.TrimSpace(g.DesiredOutcome) == "" {
		return fmt.Errorf("%w: desired outcome is required", ErrGoalInvalid)
	}
	if g.Risk == "" {
		return fmt.Errorf("%w: risk classification is required", ErrGoalInvalid)
	}
	if strings.TrimSpace(g.AuthoritySource) == "" {
		return fmt.Errorf("%w: authority source is required", ErrGoalInvalid)
	}
	return nil
}

type GoalDiff struct {
	PreviousRevision       int64        `json:"previous_revision"`
	NewRevision            int64        `json:"new_revision"`
	AddedConstraints       []Constraint `json:"added_constraints"`
	RemovedConstraints     []Constraint `json:"removed_constraints"`
	ModifiedConstraints    []Constraint `json:"modified_constraints"`
	AddedDoNotDo           []string     `json:"added_do_not_do"`
	RemovedDoNotDo         []string     `json:"removed_do_not_do"`
	ScopeChanged           bool         `json:"scope_changed"`
	SuccessCriteriaChanged bool         `json:"success_criteria_changed"`
	AffectedCriticalClaims []string     `json:"affected_critical_claims"`
	RequiresReEvaluation   bool         `json:"requires_re_evaluation"`
	SafestRollbackRevision int64        `json:"safest_rollback_revision"`
}

// ComputeGoalDiff compares two GoalContract revisions and detects changes requiring re-evaluation.
func ComputeGoalDiff(oldGoal, newGoal GoalContract) GoalDiff {
	diff := GoalDiff{
		PreviousRevision:       oldGoal.Revision,
		NewRevision:            newGoal.Revision,
		SafestRollbackRevision: oldGoal.Revision,
	}

	oldConstraints := make(map[string]Constraint)
	for _, c := range oldGoal.Constraints {
		oldConstraints[c.ID] = c
	}
	newConstraints := make(map[string]Constraint)
	for _, c := range newGoal.Constraints {
		newConstraints[c.ID] = c
		oldC, exists := oldConstraints[c.ID]
		if !exists {
			diff.AddedConstraints = append(diff.AddedConstraints, c)
			diff.RequiresReEvaluation = true
		} else if oldC.Text != c.Text || oldC.IsHard != c.IsHard || oldC.Scope != c.Scope {
			diff.ModifiedConstraints = append(diff.ModifiedConstraints, c)
			diff.RequiresReEvaluation = true
		}
	}
	for id, oldC := range oldConstraints {
		if _, exists := newConstraints[id]; !exists {
			diff.RemovedConstraints = append(diff.RemovedConstraints, oldC)
			diff.RequiresReEvaluation = true
		}
	}

	oldDND := make(map[string]bool)
	for _, d := range oldGoal.DoNotDo {
		oldDND[d] = true
	}
	newDND := make(map[string]bool)
	for _, d := range newGoal.DoNotDo {
		newDND[d] = true
		if !oldDND[d] {
			diff.AddedDoNotDo = append(diff.AddedDoNotDo, d)
			diff.RequiresReEvaluation = true
		}
	}
	for d := range oldDND {
		if !newDND[d] {
			diff.RemovedDoNotDo = append(diff.RemovedDoNotDo, d)
			diff.RequiresReEvaluation = true
		}
	}

	if strings.Join(oldGoal.Scope, ",") != strings.Join(newGoal.Scope, ",") {
		diff.ScopeChanged = true
		diff.RequiresReEvaluation = true
	}
	if strings.Join(oldGoal.SuccessCriteria, ",") != strings.Join(newGoal.SuccessCriteria, ",") {
		diff.SuccessCriteriaChanged = true
		diff.RequiresReEvaluation = true
	}

	for _, claim := range newGoal.RequiredCriticalClaims {
		diff.AffectedCriticalClaims = append(diff.AffectedCriticalClaims, claim)
	}

	return diff
}

// CanModifyGoal enforces that build-agent roles cannot remove or weaken hard operator constraints.
func CanModifyGoal(callerRole string, oldGoal, newGoal GoalContract) error {
	isAgentRole := callerRole != "operator" && callerRole != "admin" && callerRole != "user"

	if isAgentRole {
		newConstraints := make(map[string]Constraint)
		for _, c := range newGoal.Constraints {
			newConstraints[c.ID] = c
		}
		for _, oldC := range oldGoal.Constraints {
			if oldC.IsHard {
				newC, exists := newConstraints[oldC.ID]
				if !exists {
					return fmt.Errorf("%w: agent %s cannot delete hard constraint %s", ErrGoalHardConstraint, callerRole, oldC.ID)
				}
				if !newC.IsHard {
					return fmt.Errorf("%w: agent %s cannot demote hard constraint %s to soft", ErrGoalHardConstraint, callerRole, oldC.ID)
				}
				if newC.Text != oldC.Text {
					return fmt.Errorf("%w: agent %s cannot alter hard constraint %s", ErrGoalHardConstraint, callerRole, oldC.ID)
				}
			}
		}

		newDND := make(map[string]bool)
		for _, d := range newGoal.DoNotDo {
			newDND[d] = true
		}
		for _, oldD := range oldGoal.DoNotDo {
			if !newDND[oldD] {
				return fmt.Errorf("%w: agent %s cannot remove do-not-do constraint %q", ErrGoalHardConstraint, callerRole, oldD)
			}
		}
	}
	return nil
}
