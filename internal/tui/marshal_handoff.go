package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

// marshalGoalHandoff builds a concise handoff of the active goal from
// Marshal's own durable records and live Board cards. It returns ok=false when
// no goal is active or when root is not this project's root (for example a
// worker worktree), so worker launches never receive the Marshal's handoff.
func (w *Workspace) marshalGoalHandoff(root string) (app.MarshalGoalHandoff, bool) {
	var zero app.MarshalGoalHandoff
	if w == nil {
		return zero, false
	}
	owner := w.providerRoot()
	if root != "" && owner != "" && filepath.Clean(root) != filepath.Clean(owner) {
		return zero, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var (
		h  app.MarshalGoalHandoff
		ok bool
	)
	switch {
	case w.runtime != nil:
		var err error
		h, ok, err = w.runtime.MarshalGoalHandoff(ctx, w.sessionID)
		if err != nil {
			return zero, false
		}
	case w.store != nil && w.sessionID != "":
		goal, err := w.store.GetActiveGoalContract(ctx, w.sessionID)
		if err != nil || goal.Confirmation == model.ConfirmationCancelled {
			return zero, false
		}
		if term, terr := w.store.GetGoalTermination(ctx, w.sessionID, goal.ID, goal.Revision); terr == nil && term != nil {
			if term.State == model.StateSuccess || term.State == model.StateCancelled {
				return zero, false
			}
		}
		h = handoffFromGoalContract(goal)
		ok = true
	default:
		w.mu.RLock()
		goal := w.state.Goal
		w.mu.RUnlock()
		if goal.ID == "" || goal.Confirmation == model.ConfirmationCancelled {
			return zero, false
		}
		h = handoffFromGoalContract(goal)
		ok = true
	}
	if !ok {
		return zero, false
	}

	if m := w.marshalSession(); m != nil {
		m.mu.Lock()
		from := m.switchedFrom
		to := m.provider
		m.mu.Unlock()
		if from != "" {
			h.SwitchedFrom = marshalDisplayName(canonicalNeutralProvider(from))
			if to != "" {
				h.SwitchedTo = marshalDisplayName(canonicalNeutralProvider(to))
			}
		}
	}

	// Include confirmed requirements card lines (or note an open unconfirmed
	// card) from the live Board so a mid-intake switch does not lose them.
	w.req.mu.Lock()
	var confirmedCopy *[8]string
	if w.req.confirmed != nil {
		c := *w.req.confirmed
		confirmedCopy = &c
	}
	var openReq *reqCard
	if w.req.card != nil {
		c := *w.req.card
		openReq = &c
	}
	w.req.mu.Unlock()
	if confirmedCopy != nil {
		var cardLines []string
		for i, text := range *confirmedCopy {
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			cardLines = append(cardLines, fmt.Sprintf("%c (%s): %s", byte('a'+i), reqLabels[i], text))
		}
		if len(cardLines) > 0 {
			h.ConfirmedRequirements = append(cardLines, h.ConfirmedRequirements...)
			h.RequirementsConfirmed = true
		}
	} else if openReq != nil {
		h.RequirementsCardOpen = true
		h.OpenDecisions = append(h.OpenDecisions,
			fmt.Sprintf("requirements card open (%d of 8 filled), awaiting owner confirmation", openReq.filled()))
	}

	if ask := w.currentAsk(); ask != nil && strings.TrimSpace(ask.Question) != "" {
		entry := "open question waiting for owner: " + strings.TrimSpace(ask.Question)
		if len(ask.Choices) > 0 {
			labels := make([]string, 0, len(ask.Choices))
			for _, c := range ask.Choices {
				if l := strings.TrimSpace(c.Label); l != "" {
					labels = append(labels, l)
				}
			}
			if len(labels) > 0 {
				entry += " (choices: " + strings.Join(labels, ", ") + ")"
			}
		}
		h.OpenDecisions = append(h.OpenDecisions, entry)
	}

	w.change.mu.Lock()
	chg := w.change.card
	w.change.mu.Unlock()
	if chg != nil {
		if chg.ownerHold() {
			h.OpenDecisions = append(h.OpenDecisions, "owner hold active on tasks: "+strings.Join(chg.held, ", "))
		} else {
			h.OpenDecisions = append(h.OpenDecisions, "mid-run change waiting for owner (apply change or keep current goal)")
		}
	}

	app.DeriveMarshalHandoffPhase(&h)
	return h, true
}

func handoffFromGoalContract(goal model.GoalContract) app.MarshalGoalHandoff {
	h := app.MarshalGoalHandoff{
		GoalID:           goal.ID,
		GoalRevision:     goal.Revision,
		GoalText:         goal.DesiredOutcome,
		OriginalRequest:  goal.OriginalRequest,
		GoalConfirmation: goal.Confirmation,
	}
	for _, item := range goal.SuccessCriteria {
		if s := strings.TrimSpace(item); s != "" {
			h.ConfirmedRequirements = append(h.ConfirmedRequirements, "done when: "+s)
		}
	}
	for _, item := range goal.Scope {
		if s := strings.TrimSpace(item); s != "" {
			h.ConfirmedRequirements = append(h.ConfirmedRequirements, "scope: "+s)
		}
	}
	for _, c := range goal.Constraints {
		if s := strings.TrimSpace(c.Text); s != "" {
			h.ConfirmedRequirements = append(h.ConfirmedRequirements, "constraint: "+s)
		}
	}
	for _, item := range goal.DoNotDo {
		if s := strings.TrimSpace(item); s != "" {
			h.ConfirmedRequirements = append(h.ConfirmedRequirements, "do not: "+s)
		}
	}
	if goal.Confirmation.Settled() && len(h.ConfirmedRequirements) > 0 {
		h.RequirementsConfirmed = true
	}
	for _, d := range goal.UnresolvedDecisions {
		if q := strings.TrimSpace(d.Question); q != "" {
			h.OpenDecisions = append(h.OpenDecisions, q)
		}
	}
	app.DeriveMarshalHandoffPhase(&h)
	return h
}

func (w *Workspace) marshalGoalHandoffBlock(root string) string {
	h, ok := w.marshalGoalHandoff(root)
	if !ok {
		return ""
	}
	text := RedactContent(app.FormatMarshalHandoff(h), nil)
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return "\n" + text + "\n"
}
