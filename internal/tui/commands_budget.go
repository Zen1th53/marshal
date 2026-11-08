package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/google/uuid"
)

// budgetLimitsAndRuns appends the goal revision's limits and the per-run
// consumption they are enforced against, before each task is dispatched.
func (h *CommandHandler) budgetLimitsAndRuns(ctx context.Context) string {
	h.ws.mu.RLock()
	goal := h.ws.state.Goal
	h.ws.mu.RUnlock()
	var b strings.Builder
	if goal.ID == "" {
		b.WriteString("\nLIMITS: no active goal")
	} else if goal.BudgetLimits == nil {
		fmt.Fprintf(&b, "\nLIMITS (goal %s rev %d): none set", goal.ID, goal.Revision)
	} else {
		fmt.Fprintf(&b, "\nLIMITS (goal %s rev %d; enforced per run before each task):", goal.ID, goal.Revision)
		if goal.BudgetLimits.MaxModelCalls > 0 {
			fmt.Fprintf(&b, "\n  Model calls: %d", goal.BudgetLimits.MaxModelCalls)
		}
		if goal.BudgetLimits.MaxDuration > 0 {
			fmt.Fprintf(&b, "\n  Duration:    %s", goal.BudgetLimits.MaxDuration)
		}
	}
	_, runs, err := h.sessionRuns(ctx)
	if err == nil && len(runs) > 0 {
		b.WriteString("\nRUN CONSUMPTION (counted by the engine):")
		for _, run := range runs {
			fmt.Fprintf(&b, "\n  %s %s: %d model calls, running for %s", run.RunID, run.State, run.BudgetConsumed.ModelCalls, time.Since(run.StartedAt).Round(time.Second))
		}
	}
	return b.String()
}

// handleBudgetSet changes the goal's budget limits by revising the goal, the
// only place a limit is honoured. All six enforced dimensions are accepted and
// set as one full-replace revision through CommandSetGoalBudget; token and cost
// ceilings are enforced (fail closed when usage is unreported) rather than
// guessed.
func (h *CommandHandler) handleBudgetSet(ctx context.Context, args []string) (string, error) {
	a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority)
	if !ok || a == nil || a.runtime == nil || a.localControl == nil {
		return "Budget limits were NOT changed: authenticated runtime authorization is required.", nil
	}
	if len(args) > 0 && strings.EqualFold(args[0], "set") {
		args = args[1:]
	}
	const usage = "Usage: /budget set [calls=<n>] [duration=<go duration>] [tokens=<n>] [cost=<n>] [handoffs=<n>] [reserve=<n>]"
	var limits model.BudgetLimit
	for _, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			return usage, nil
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "calls":
			n, err := strconv.Atoi(value)
			if err != nil || n <= 0 {
				return "Budget limits were NOT changed: calls must be a positive whole number.", nil
			}
			limits.MaxModelCalls = n
		case "duration":
			d, err := time.ParseDuration(value)
			if err != nil || d <= 0 {
				return "Budget limits were NOT changed: duration must be a positive Go duration such as 30m or 2h.", nil
			}
			limits.MaxDuration = d
		case "tokens":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n <= 0 {
				return "Budget limits were NOT changed: tokens must be a positive whole number.", nil
			}
			limits.MaxTotalTokens = &n
		case "cost":
			f, err := strconv.ParseFloat(value, 64)
			if err != nil || f <= 0 {
				return "Budget limits were NOT changed: cost must be a positive number.", nil
			}
			limits.MaxCostUSD = &f
		case "handoffs":
			n, err := strconv.Atoi(value)
			if err != nil || n <= 0 {
				return "Budget limits were NOT changed: handoffs must be a positive whole number.", nil
			}
			limits.MaxHandoffs = n
		case "reserve":
			n, err := strconv.Atoi(value)
			if err != nil || n <= 0 {
				return "Budget limits were NOT changed: reserve must be a positive whole number.", nil
			}
			limits.VerificationReserveCalls = n
		default:
			return usage, nil
		}
	}
	if limits.Empty() {
		return usage, nil
	}
	h.ws.mu.RLock()
	goal := h.ws.state.Goal
	h.ws.mu.RUnlock()
	if goal.ID == "" {
		return "Budget limits were NOT changed: there is no active goal to revise.", nil
	}
	e := app.CommandEnvelope{ProjectID: a.runtime.ProjectIdentity(), SessionID: a.sessionID, TargetID: goal.ID,
		ExpectedVersion: goal.Revision, IdempotencyKey: uuid.NewString()}
	revised, err := a.runtime.CommandSetGoalBudget(a.localControl.Context(ctx), e, limits)
	if err != nil {
		return fmt.Sprintf("Budget limits were NOT changed: %v", err), nil
	}
	return fmt.Sprintf("Goal %s revised to revision %d with the new budget limits; it is PENDING until you confirm it with /approve goal:%s@%d.",
		revised.ID, revised.Revision, revised.ID, revised.Revision), nil
}
