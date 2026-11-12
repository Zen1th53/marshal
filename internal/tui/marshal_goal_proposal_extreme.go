//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/google/uuid"
)

// marshalGoalNextStep tells the Marshal how to establish a missing Goal.
const marshalGoalNextStep = `No Goal exists yet. Formalize the owner's request: call marshal_proposal_submit with proposal {"action":"goal","value":"<desired outcome, one line>","criteria":"<JSON array of success criteria strings>"}; the runtime creates the Goal and records your confirmation. Then call marshal_project_contract_context again.`

// marshalGoalCriteria decodes the optional criteria field of a goal
// proposal: a JSON array of 1..12 non-empty single-line strings.
func marshalGoalCriteria(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var criteria []string
	if err := json.Unmarshal([]byte(raw), &criteria); err != nil {
		return nil, fmt.Errorf("criteria must be a JSON array of strings: %w", err)
	}
	if len(criteria) == 0 || len(criteria) > 12 {
		return nil, errors.New("criteria must list 1 to 12 success criteria")
	}
	for i, criterion := range criteria {
		criterion = strings.TrimSpace(criterion)
		if criterion == "" || len(criterion) > 500 || strings.ContainsFunc(criterion, unicode.IsControl) {
			return nil, errors.New("each criterion must be one non-empty line of at most 500 bytes")
		}
		criteria[i] = criterion
	}
	return criteria, nil
}

// applyMarshalGoalProposal lets the bound live Marshal formalize the owner's
// request as the Goal and confirm it. In this edition the Marshal decides
// approvals, so the confirmation is applied through the canonical decision
// path and recorded as the Marshal's decision; the owner never has to run
// /goal or Control > Approvals.
func (w *Workspace) applyMarshalGoalProposal(p marshalProposal) {
	ctx := w.workers.context()
	goal, err := w.formalizeMarshalGoal(ctx, p)
	outcome := "applied"
	note := ""
	if err != nil {
		outcome = "rejected"
		note = "Marshal goal proposal refused: " + RedactContent(err.Error(), nil)
	} else {
		// Activity names the goal by what it says, never by its ID.
		note = "The Marshal set the goal: " + RedactContent(goal.DesiredOutcome, nil)
	}
	if w.store != nil && p.occurrence != "" {
		_ = w.store.ResolveMarshalProposal(context.Background(), w.projectID, p.occurrence, outcome)
	}
	w.RecordActivity(note)
}

func (w *Workspace) formalizeMarshalGoal(ctx context.Context, p marshalProposal) (model.GoalContract, error) {
	if p.who != "Marshal" {
		return model.GoalContract{}, errors.New("only the bound live Marshal may formalize the Goal")
	}
	criteria, err := marshalGoalCriteria(p.criteria)
	if err != nil {
		return model.GoalContract{}, err
	}
	outcome := strings.TrimSpace(p.value)
	authority, ok := w.controlSource().Authority.(*runtimeControlAuthority)
	if !ok || authority == nil || authority.runtime == nil {
		return model.GoalContract{}, errors.New("goal formalization requires the authenticated runtime")
	}
	if authority.localControlErr != nil {
		return model.GoalContract{}, authority.localControlErr
	}
	current, err := authority.CurrentGoal(ctx)
	switch {
	case errors.Is(err, model.ErrGoalNotFound) || (err == nil && current.Confirmation == model.ConfirmationCancelled):
		envelope := app.CommandEnvelope{ProjectID: authority.runtime.ProjectIdentity(), SessionID: w.sessionID, TargetID: "GOAL-" + uuid.NewString(), IdempotencyKey: uuid.NewString()}
		if current, err = authority.GoalMutation(ctx, envelope, "create", outcome); err != nil {
			return model.GoalContract{}, fmt.Errorf("create Goal: %w", err)
		}
		if criteria != nil {
			if current, err = w.reviseMarshalGoal(ctx, authority, current, "", criteria); err != nil {
				return model.GoalContract{}, err
			}
		}
	case err != nil:
		return model.GoalContract{}, err
	default:
		desired := ""
		if outcome != current.DesiredOutcome {
			desired = outcome
		}
		if desired != "" || (criteria != nil && !equalStrings(criteria, current.SuccessCriteria)) {
			if current, err = w.reviseMarshalGoal(ctx, authority, current, desired, criteria); err != nil {
				return model.GoalContract{}, err
			}
		}
	}
	if !current.Confirmation.Settled() {
		const rationale = "Marshal: the Marshal confirmed the Goal it formalized from the owner's request"
		if err := authority.DecideApprovalTarget(ctx, approvalTarget(goalApprovalRecord(current)), true, rationale); err != nil {
			return model.GoalContract{}, fmt.Errorf("confirm Goal %s rev %d: %w", current.ID, current.Revision, err)
		}
		confirmed, err := authority.CurrentGoal(ctx)
		if err != nil {
			return model.GoalContract{}, err
		}
		if confirmed.ID != current.ID || !confirmed.Confirmation.Settled() {
			return model.GoalContract{}, fmt.Errorf("Goal %s is not confirmed after the Marshal decision", current.ID)
		}
		current = confirmed
		at := time.Now().UTC()
		id := fmt.Sprintf("marshal-goal-confirm:%s@%d", current.ID, current.Revision)
		if _, err := authority.runtime.EmitEvent(ctx, events.Event{ID: id, Type: events.EventTypeApprovalGranted, Subject: current.ID, ResourceID: current.ID, At: at, IdempotencyKey: id, Data: map[string]any{
			"operation": "GOAL_CONFIRM", "decider": "Marshal", "goal_revision": current.Revision,
			"proposal": p.occurrence, "source_session": p.sourceSession, "reason": rationale,
		}}); err != nil {
			return model.GoalContract{}, fmt.Errorf("record the Marshal's Goal confirmation: %w", err)
		}
	}
	w.mu.Lock()
	w.state.Goal = current
	w.mu.Unlock()
	return current, nil
}

func (w *Workspace) reviseMarshalGoal(ctx context.Context, authority *runtimeControlAuthority, current model.GoalContract, outcome string, criteria []string) (model.GoalContract, error) {
	envelope := app.CommandEnvelope{ProjectID: authority.runtime.ProjectIdentity(), SessionID: w.sessionID, TargetID: current.ID, ExpectedVersion: current.Revision, IdempotencyKey: uuid.NewString()}
	revised, err := authority.runtime.CommandEditGoal(authority.localControl.Context(ctx), envelope, app.GoalEdit{DesiredOutcome: outcome, SuccessCriteria: criteria, Reason: "Marshal formalized the owner's request"})
	if err != nil {
		return model.GoalContract{}, fmt.Errorf("revise Goal %s: %w", current.ID, err)
	}
	return revised, nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
