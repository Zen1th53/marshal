package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
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

const goalUsage = "Usage: /goal create <request> | edit <outcome> | cancel <reason> | constraints | add-constraint <text> | rm-constraint <id|text> | version [revision] | diff [from to] | criteria | donotdo | progress"

// The adapter supplies only the exact envelope and input. Formation, provenance,
// constraint revisions, receipts and read-back belong to the application.
func (h *CommandHandler) handleGoalMutation(ctx context.Context, verb, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return goalUsage, nil
	}
	source := h.ws.controlSource()
	authority, ok := source.Authority.(*runtimeControlAuthority)
	if !ok || authority == nil || authority.runtime == nil {
		return msgNothingChanged + "\n" + goalUsage, nil
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

// handleGoalShortForm implements "/goal <desired outcome>": it creates the
// Goal when there is no active one (or it was cancelled), through the same
// canonical mutation as /goal create. Free text never silently rewrites an
// active Goal; revising one stays the explicit /goal edit.
func (h *CommandHandler) handleGoalShortForm(ctx context.Context, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return goalUsage, nil
	}
	if source := h.ws.controlSource(); source != nil {
		if authority, ok := source.Authority.(*runtimeControlAuthority); ok && authority != nil && authority.runtime != nil && authority.localControlErr == nil {
			goal, err := authority.CurrentGoal(ctx)
			switch {
			case errors.Is(err, model.ErrGoalNotFound) || (err == nil && goal.Confirmation == model.ConfirmationCancelled):
				return h.handleGoalMutation(ctx, "create", text)
			case err != nil:
				return "", err
			default:
				return fmt.Sprintf("Goal mutation is unavailable in TUI for free text while Goal %s is active; revise it with /goal edit <outcome>.\n%s", goal.ID, goalUsage), nil
			}
		}
	}
	return h.handleGoalMutation(ctx, "create", text)
}

func (h *CommandHandler) handleGoalAddConstraint(ctx context.Context, text string) (string, error) {
	return h.handleGoalMutation(ctx, "add-constraint", text)
}
func (h *CommandHandler) handleGoalRemoveConstraint(ctx context.Context, text string) (string, error) {
	return h.handleGoalMutation(ctx, "rm-constraint", text)
}

// handleGoalCancel confirms /goal cancel in the composer, the same two-step way
// /ultra stop does. It never cancels directly: /goal cancel <reason> reads and
// remembers the exact goal revision and asks the person to type /goal cancel
// confirm, and only that confirmation submits the prepared, revision-bound
// cancellation through the CTUI-0048 binding — the single governed path where
// the re-read and the idempotency key keep the enforcement intact.
func (h *CommandHandler) handleGoalCancel(ctx context.Context, reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "Usage: /goal cancel <reason>", nil
	}
	source := h.ws.controlSource()
	if _, ok := source.Authority.(goalCancellationAuthority); !ok {
		return msgNothingChanged, nil
	}
	goal, err := source.Authority.CurrentGoal(ctx)
	if err != nil && !errors.Is(err, model.ErrGoalNotFound) {
		return "", err
	}
	if goal.ID == "" {
		return "No active goal to cancel.", nil
	}
	binding, ok := source.Bindings()[ActionID("CTUI-0048")]
	if !ok || !binding.Bound() {
		return "Goal cancellation isn't available in this version.", nil
	}

	if strings.EqualFold(reason, "confirm") {
		p := h.ws.takeGoalCancel()
		if p == nil {
			return "Nothing to confirm. Type /goal cancel <reason> first.", nil
		}
		current, err := binding.Prepare(ctx)
		if err != nil {
			return "The goal changed since you asked, so nothing was cancelled. Type /goal cancel <reason> again.", nil
		}
		if moved, _ := targetMoved(p.target, current); moved {
			return "The goal changed since you asked, so nothing was cancelled. Type /goal cancel <reason> again.", nil
		}
		req := ActionRequest{Action: binding.Action, Title: binding.Title, Safety: binding.Safety, Target: current,
			SessionID: source.SessionID, ProjectID: source.ProjectID, Inputs: map[string]string{"reason": p.reason},
			IdempotencyKey: p.key, PreparedAt: time.Now()}
		outcome, err := binding.Execute(ctx, req)
		if err == nil && outcome.Succeeded() {
			return "The goal was cancelled.", nil
		}
		detail := firstLine(outcome.Detail)
		if detail == "" && err != nil {
			detail = firstLine(err.Error())
		}
		return "The goal was not cancelled: " + detail, nil
	}

	target, err := binding.Prepare(ctx)
	if err != nil {
		return "The goal can't be cancelled right now: " + firstLine(err.Error()), nil
	}
	h.ws.askGoalCancel(&pendingGoalCancel{target: target, reason: reason, key: uuid.NewString()})
	return fmt.Sprintf("This cancels the current goal: \"%s\".\n"+
		"  Type /goal cancel confirm to cancel it. Any other command keeps the goal.",
		goalCancelSummary(goal.DesiredOutcome)), nil
}

// goalCancelSummary renders the goal statement for the confirmation prompt: its
// first line, at most 80 runes, with a trailing … when it was cut.
func goalCancelSummary(outcome string) string {
	r := []rune(firstLine(outcome))
	if len(r) <= 80 {
		return string(r)
	}
	return string(r[:79]) + "…"
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
