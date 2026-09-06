package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// Goal constraint operations.
//
// Constraints are part of the GoalContract, so adding or removing one is a
// revision of that contract written through the canonical store, not a
// TUI-local note. Previously "/goal add-constraint no-net" fell through to the
// free-text branch and overwrote the desired outcome with the literal string
// "add-constraint no-net", destroying the goal statement.

// handleGoalConstraints lists the constraints bound to the active goal.
func (h *CommandHandler) handleGoalConstraints(ctx context.Context) (string, error) {
	h.ws.mu.RLock()
	goal := h.ws.state.Goal
	h.ws.mu.RUnlock()

	if goal.ID == "" {
		return "No active goal. Set one with /goal <outcome> before adding constraints.", nil
	}
	if len(goal.Constraints) == 0 {
		return fmt.Sprintf("Goal %s [rev %d] has no constraints.\nAdd one with /goal add-constraint <text>.",
			goal.ID, goal.Revision), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("CONSTRAINTS — %s [rev %d] (%d):\n", goal.ID, goal.Revision, len(goal.Constraints)))
	for _, c := range goal.Constraints {
		kind := "guideline"
		if c.IsHard {
			kind = "hard"
		}
		b.WriteString(fmt.Sprintf("  %-14s %-10s %s\n", c.ID, kind, RedactContent(c.Text, h.ws.state.KnownSecrets)))
	}
	return b.String(), nil
}

// handleGoalAddConstraint appends a constraint and saves a new goal revision.
func (h *CommandHandler) handleGoalAddConstraint(ctx context.Context, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "Usage: /goal add-constraint <constraint text>", nil
	}
	if h.ws.store == nil {
		return "Store unavailable", nil
	}

	h.ws.mu.Lock()
	defer h.ws.mu.Unlock()

	goal := h.ws.state.Goal
	if goal.ID == "" {
		return "No active goal. Set one with /goal <outcome> before adding constraints.", nil
	}

	for _, existing := range goal.Constraints {
		if strings.EqualFold(strings.TrimSpace(existing.Text), text) {
			return fmt.Sprintf("Constraint already present on %s [rev %d]: %s",
				goal.ID, goal.Revision, existing.ID), nil
		}
	}

	expectedRev := goal.Revision
	constraint := model.Constraint{
		ID:     fmt.Sprintf("c-%d", time.Now().UnixNano()),
		Text:   text,
		Source: "operator",
		IsHard: true,
	}

	updated := goal
	updated.Constraints = append(append([]model.Constraint{}, goal.Constraints...), constraint)
	updated.Revision = expectedRev + 1
	updated.UpdatedAt = time.Now().UTC()

	if err := h.ws.store.SaveGoalContract(ctx, updated, expectedRev); err != nil {
		return "", fmt.Errorf("add constraint: %w", err)
	}

	// Read back so the reported revision is the stored one, not an assumption.
	stored, err := h.ws.store.GetActiveGoalContract(ctx, h.ws.sessionID)
	if err != nil {
		return "", fmt.Errorf("read back goal: %w", err)
	}
	h.ws.state.Goal = stored

	return fmt.Sprintf("Constraint %s added to %s [rev %d]: %s\nOutcome unchanged: %s",
		constraint.ID, stored.ID, stored.Revision,
		RedactContent(text, h.ws.state.KnownSecrets),
		RedactContent(stored.DesiredOutcome, h.ws.state.KnownSecrets)), nil
}

// handleGoalRemoveConstraint drops a constraint by id or exact text.
func (h *CommandHandler) handleGoalRemoveConstraint(ctx context.Context, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "Usage: /goal rm-constraint <constraint_id|text>", nil
	}
	if h.ws.store == nil {
		return "Store unavailable", nil
	}

	h.ws.mu.Lock()
	defer h.ws.mu.Unlock()

	goal := h.ws.state.Goal
	if goal.ID == "" {
		return "No active goal.", nil
	}

	var kept []model.Constraint
	var removed *model.Constraint
	for _, c := range goal.Constraints {
		if removed == nil && (c.ID == target || strings.EqualFold(strings.TrimSpace(c.Text), target)) {
			copyC := c
			removed = &copyC
			continue
		}
		kept = append(kept, c)
	}
	if removed == nil {
		return fmt.Sprintf("No constraint matching %q on %s [rev %d].", target, goal.ID, goal.Revision), nil
	}

	expectedRev := goal.Revision
	updated := goal
	updated.Constraints = kept
	updated.Revision = expectedRev + 1
	updated.UpdatedAt = time.Now().UTC()

	if err := h.ws.store.SaveGoalContract(ctx, updated, expectedRev); err != nil {
		return "", fmt.Errorf("remove constraint: %w", err)
	}

	stored, err := h.ws.store.GetActiveGoalContract(ctx, h.ws.sessionID)
	if err != nil {
		return "", fmt.Errorf("read back goal: %w", err)
	}
	h.ws.state.Goal = stored

	return fmt.Sprintf("Constraint %s removed from %s [rev %d]. %d remaining.",
		removed.ID, stored.ID, stored.Revision, len(stored.Constraints)), nil
}
