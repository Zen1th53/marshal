package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// GoalEdit is a patch: nil slices preserve their canonical fields; empty slices
// explicitly clear them. Identity, provenance and confirmation are not inputs.
type GoalEdit struct {
	DesiredOutcome  string
	SuccessCriteria []string
	DoNotDo         []string
	Constraints     []model.Constraint
	Reason          string
}

func (r *Runtime) CommandCreateGoal(ctx context.Context, e CommandEnvelope, request string) (model.GoalContract, error) {
	return r.commandGoal(ctx, e, "goal.create", request, func(ctx context.Context, actor string) (model.GoalContract, error) {
		intake, err := goalintake.Form(goalintake.FormationRequest{Request: request, ProjectID: projectid.ID(r.ProjectIdentity()), SessionID: e.SessionID, Version: constitution.Current})
		if err != nil {
			return model.GoalContract{}, err
		}
		assessment := map[string]string{}
		for name, level := range intake.Assessment.Dimensions() {
			assessment[name] = string(level)
		}
		// Recovery and scope are not established here; retain a conservative
		// risk class and the independent canonical assessment dimensions.
		g := model.GoalContract{ID: e.TargetID, SessionID: e.SessionID, ProjectID: string(intake.ProjectID), OriginalRequest: intake.OriginalRequest, RequestDigest: intake.RequestDigest, ConstitutionVersion: intake.Version.String(), Confirmation: model.ConfirmationState(intake.Confirmation), Assessment: assessment, Revision: 1, DesiredOutcome: intake.Interpretation, Constraints: intake.Constraints, SuccessCriteria: intake.AcceptanceCriteria, DoNotDo: intake.OutOfScope, Assumptions: intake.Assumptions, UnresolvedDecisions: intake.Ambiguities, Risk: model.R3, AuthoritySource: actor, CreatedAt: intake.FormedAt, UpdatedAt: intake.FormedAt}
		g.EvaluateUnderstanding()
		if err := r.store.SaveGoalContract(ctx, g, 0); err != nil {
			return model.GoalContract{}, err
		}
		return r.store.GetGoalContract(ctx, g.ID, 1)
	})
}

func (r *Runtime) CommandEditGoal(ctx context.Context, e CommandEnvelope, edit GoalEdit) (model.GoalContract, error) {
	return r.commandGoal(ctx, e, "goal.edit", edit, func(ctx context.Context, actor string) (model.GoalContract, error) { return r.editGoal(ctx, e, edit) })
}

func (r *Runtime) editGoal(ctx context.Context, e CommandEnvelope, edit GoalEdit) (model.GoalContract, error) {
	if strings.TrimSpace(edit.Reason) == "" {
		return model.GoalContract{}, fmt.Errorf("%w: revision reason is required", model.ErrGoalInvalid)
	}
	g, err := r.store.GetActiveGoalContract(ctx, e.SessionID)
	if err != nil {
		return model.GoalContract{}, err
	}
	if g.Revision != e.ExpectedVersion {
		return model.GoalContract{}, model.ErrGoalConflict
	}
	if strings.TrimSpace(edit.DesiredOutcome) != "" {
		g.DesiredOutcome = strings.TrimSpace(edit.DesiredOutcome)
	}
	if edit.SuccessCriteria != nil {
		g.SuccessCriteria = append([]string{}, edit.SuccessCriteria...)
	}
	if edit.DoNotDo != nil {
		g.DoNotDo = append([]string{}, edit.DoNotDo...)
	}
	if edit.Constraints != nil {
		seen := map[string]bool{}
		for _, c := range edit.Constraints {
			if c.ID == "" || strings.TrimSpace(c.Text) == "" || seen[c.ID] {
				return model.GoalContract{}, model.ErrGoalInvalid
			}
			seen[c.ID] = true
		}
		previousConstraints := g.Constraints
		g.Constraints = append([]model.Constraint{}, edit.Constraints...)
		owner, _ := auth.LocalFromContext(ctx)
		sources := map[string]string{}
		for _, c := range previousConstraints {
			sources[c.ID] = c.Source
		}
		for i := range g.Constraints {
			if source, ok := sources[g.Constraints[i].ID]; ok {
				g.Constraints[i].Source = source
			} else {
				g.Constraints[i].Source = owner.ID()
			}
		}
	}
	g.Confirmation = model.ConfirmationPending
	g.Revision++
	g.RevisionReason = edit.Reason
	g.UpdatedAt = time.Now().UTC()
	g.EvaluateUnderstanding()
	if err := r.store.SaveGoalContract(ctx, g, e.ExpectedVersion); err != nil {
		return model.GoalContract{}, err
	}
	return r.store.GetGoalContract(ctx, g.ID, g.Revision)
}

func (r *Runtime) CommandAddGoalConstraint(ctx context.Context, e CommandEnvelope, text string) (model.GoalContract, error) {
	return r.commandGoal(ctx, e, "goal.add-constraint", text, func(ctx context.Context, actor string) (model.GoalContract, error) {
		if strings.TrimSpace(text) == "" {
			return model.GoalContract{}, model.ErrGoalInvalid
		}
		g, err := r.store.GetActiveGoalContract(ctx, e.SessionID)
		if err != nil {
			return model.GoalContract{}, err
		}
		sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(text))))
		c := model.Constraint{ID: "CONSTRAINT-" + hex.EncodeToString(sum[:6]), Text: strings.TrimSpace(text), Source: actor, IsHard: true}
		for _, old := range g.Constraints {
			if old.ID == c.ID || strings.EqualFold(strings.TrimSpace(old.Text), c.Text) {
				return model.GoalContract{}, fmt.Errorf("%w: constraint already present: %s", model.ErrConflict, old.ID)
			}
		}
		return r.editGoal(ctx, e, GoalEdit{Constraints: append(append([]model.Constraint{}, g.Constraints...), c), Reason: "owner added constraint"})
	})
}

func (r *Runtime) CommandRemoveGoalConstraint(ctx context.Context, e CommandEnvelope, target string) (model.GoalContract, error) {
	return r.commandGoal(ctx, e, "goal.rm-constraint", target, func(ctx context.Context, actor string) (model.GoalContract, error) {
		g, err := r.store.GetActiveGoalContract(ctx, e.SessionID)
		if err != nil {
			return model.GoalContract{}, err
		}
		kept := []model.Constraint{}
		found := false
		for _, c := range g.Constraints {
			if !found && (c.ID == strings.TrimSpace(target) || strings.EqualFold(c.Text, strings.TrimSpace(target))) {
				found = true
				continue
			}
			kept = append(kept, c)
		}
		if !found {
			return model.GoalContract{}, fmt.Errorf("%w: constraint not found", model.ErrNotFound)
		}
		return r.editGoal(ctx, e, GoalEdit{Constraints: kept, Reason: "owner explicitly removed constraint"})
	})
}
