package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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

const goalUsage = "Usage: /goal create <request> | edit <outcome> | constraints | add-constraint <text> | rm-constraint <id|text> | version [revision] | diff [from to] | criteria | donotdo | progress"

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

func (h *CommandHandler) handleGoalReport(ctx context.Context, verb string, args []string) (string, error) {
	revisions := []int64{}
	expected := 0
	if verb == "version" {
		expected = 1
	}
	if verb == "diff" {
		expected = 2
	}
	if len(args) != 0 && len(args) != expected {
		return goalUsage, nil
	}
	for _, arg := range args {
		revision, err := strconv.ParseInt(arg, 10, 64)
		if err != nil || revision < 1 {
			return "Revision must be a positive integer.\n" + goalUsage, nil
		}
		revisions = append(revisions, revision)
	}
	authority, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority)
	if !ok || authority == nil {
		return "Canonical goal reporting is unavailable: no runtime authority.", nil
	}
	goal, err := authority.CurrentGoal(ctx)
	if errors.Is(err, model.ErrGoalNotFound) {
		return "No active goal. Use /goal create <request>.", nil
	}
	if err != nil {
		return "", err
	}
	h.ws.mu.RLock()
	secrets := append([]string{}, h.ws.state.KnownSecrets...)
	h.ws.mu.RUnlock()
	render := func(v any) (string, error) {
		data, err := json.MarshalIndent(v, "", "  ")
		return RedactContent(string(data), secrets), err
	}
	read := func(rev int64) (model.GoalContract, error) {
		g, err := authority.GoalRevision(ctx, goal.ID, rev)
		if err != nil {
			return g, fmt.Errorf("goal revision %d: %w", rev, err)
		}
		return g, nil
	}
	switch verb {
	case "version":
		if len(revisions) > 0 {
			goal, err = read(revisions[0])
			if err != nil {
				return "", err
			}
		}
		return render(goal)
	case "diff":
		if len(revisions) == 0 {
			if goal.Revision == 1 {
				return "No previous goal revision to compare. Use /goal diff <from> <to>.", nil
			}
			revisions = []int64{goal.Revision - 1, goal.Revision}
		}
		from, err := read(revisions[0])
		if err != nil {
			return "", err
		}
		to, err := read(revisions[1])
		if err != nil {
			return "", err
		}
		diff := model.ComputeGoalDiff(from, to)
		return render(struct {
			Diff                   model.GoalDiff
			FromOutcome, ToOutcome string
		}{diff, from.DesiredOutcome, to.DesiredOutcome})
	case "criteria":
		return render(goal.SuccessCriteria)
	case "donotdo":
		return render(goal.DoNotDo)
	case "progress":
		progress, err := authority.GoalProgress(ctx)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "GOAL PROGRESS [rev %d]\n", progress.Goal.Revision)
		if len(progress.Criteria) == 0 {
			b.WriteString("No success criteria.\n")
		}
		for _, row := range progress.Criteria {
			fmt.Fprintf(&b, "%s: %s — %s; evidence: %v\n", row.Criterion, row.Status, row.Detail, row.EvidenceIDs)
		}
		return RedactContent(b.String(), secrets), nil
	}
	return goalUsage, nil
}
