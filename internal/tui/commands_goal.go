package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/google/uuid"
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
		return "No active goal. Use /goal create <request>.", nil
	}
	if len(goal.Constraints) == 0 {
		return fmt.Sprintf("Goal %s [rev %d] has no constraints.",
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

const goalUsage = "Usage: /goal create <request> | edit <outcome> | constraints | add-constraint <text> | rm-constraint <id|text>"

// The adapter supplies only the exact envelope and input. Formation, provenance,
// constraint revisions, receipts and read-back belong to the application.
func (h *CommandHandler) handleGoalMutation(ctx context.Context, verb, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return goalUsage, nil
	}
	source := h.ws.controlSource()
	authority, ok := source.Authority.(*runtimeControlAuthority)
	if !ok || authority == nil || authority.runtime == nil {
		return "Goal mutation is unavailable in TUI: authenticated runtime authorization is required.\n" + goalUsage, nil
	}
	if authority.localControlErr != nil {
		return "", authority.localControlErr
	}
	goal, err := authority.CurrentGoal(ctx)
	if err != nil && !errors.Is(err, model.ErrGoalNotFound) {
		return "", err
	}
	key := uuid.NewString()
	envelope := app.CommandEnvelope{ProjectID: authority.runtime.ProjectIdentity(), SessionID: h.ws.sessionID, TargetID: goal.ID, ExpectedVersion: goal.Revision, IdempotencyKey: key}
	if verb == "create" {
		envelope.TargetID = "GOAL-" + uuid.NewString()
		envelope.ExpectedVersion = 0
	}
	stored, err := authority.GoalMutation(ctx, envelope, verb, text)
	if err != nil {
		return "", err
	}
	h.ws.mu.Lock()
	h.ws.state.Goal = stored
	secrets := append([]string{}, h.ws.state.KnownSecrets...)
	h.ws.mu.Unlock()
	return fmt.Sprintf("Goal %s [rev %d] %s: %s", stored.ID, stored.Revision, stored.Confirmation, RedactContent(stored.DesiredOutcome, secrets)), nil
}

func (h *CommandHandler) handleGoalAddConstraint(ctx context.Context, text string) (string, error) {
	return h.handleGoalMutation(ctx, "add-constraint", text)
}
func (h *CommandHandler) handleGoalRemoveConstraint(ctx context.Context, text string) (string, error) {
	return h.handleGoalMutation(ctx, "rm-constraint", text)
}

// goalCommandText removes the command and verb, preserving the argument's
// interior whitespace rather than reconstructing the original request from fields.
func goalCommandText(line string) string {
	for i := 0; i < 2; i++ {
		line = strings.TrimLeftFunc(line, unicode.IsSpace)
		index := strings.IndexFunc(line, unicode.IsSpace)
		if index < 0 {
			return ""
		}
		line = line[index:]
	}
	return strings.TrimLeftFunc(line, unicode.IsSpace)
}
