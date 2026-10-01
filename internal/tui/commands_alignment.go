package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/alignment"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/google/uuid"
)

// handleAlignment reads back the alignment guard's advisory results for this
// session's tasks and records operator decisions against violations.
func (h *CommandHandler) handleAlignment(ctx context.Context, args []string) (string, error) {
	sub := "status"
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	if sub == "resolve" {
		return h.resolveAlignment(ctx, args[1], args[2], strings.Join(args[3:], " "))
	}
	a, runs, err := h.sessionRuns(ctx)
	if err != nil {
		return "", fmt.Errorf("read runs: %w; reopen the TUI and check /store", err)
	}
	if a == nil {
		return "ALIGNMENT GUARD: NOT VERIFIED (TUI is not connected to a runtime).", nil
	}
	filter := map[string]alignment.CheckType{"scope": alignment.CheckScopeLock, "blast": alignment.CheckBlastRadius, "deletions": alignment.CheckDeletionAsSatisfaction}[sub]
	var b strings.Builder
	b.WriteString("ALIGNMENT GUARD (advisory: records violations, does not block runs):\n")
	evaluated := 0
	for _, run := range runs {
		for id, task := range run.Tasks {
			record := task.Alignment
			if record == nil {
				continue
			}
			evaluated++
			blocking := 0
			for _, v := range record.Result.Violations {
				if v.Severity == "BLOCKING" {
					blocking++
				}
			}
			verdict := "PASSED"
			if !record.Result.Passed || len(record.Result.Violations) > 0 {
				verdict = fmt.Sprintf("%d violations (%d BLOCKING)", len(record.Result.Violations), blocking)
			}
			fmt.Fprintf(&b, "  run:%s/%s  %s  (%d of %d predicted files changed; evaluated %s)\n", run.RunID, id, verdict,
				record.Result.ObservedRadius, record.Result.PredictedRadius, record.Result.EvaluatedAt.Format("2006-01-02 15:04"))
			if sub == "status" {
				continue
			}
			for i, v := range record.Result.Violations {
				if filter != "" && v.Type != filter {
					continue
				}
				path := ""
				if v.Path != "" {
					path = " " + v.Path
				}
				fmt.Fprintf(&b, "    #%d [%s] %s%s: %s\n", i, v.Severity, v.Type, path, RedactContent(v.Message, nil))
				for _, d := range record.Decisions {
					if d.Violation == i {
						fmt.Fprintf(&b, "       decision %s by %s: %s\n", d.Decision, d.Actor, RedactContent(d.Reason, nil))
					}
				}
			}
		}
	}
	if evaluated == 0 {
		b.WriteString("  No task in this session has been evaluated yet; tasks are checked when they finish.")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (h *CommandHandler) resolveAlignment(ctx context.Context, target, decision, reason string) (string, error) {
	a, runs, err := h.sessionRuns(ctx)
	if err != nil {
		return "", fmt.Errorf("read runs: %w", err)
	}
	if a == nil || a.localControl == nil {
		return "Alignment escalation was NOT resolved: authenticated runtime authorization is required.", nil
	}
	runID, _, _, err := app.ParseAlignmentTarget(target)
	if err != nil {
		return "Alignment escalation was NOT resolved: the target must be run:<run>/<task>#<n> as /alignment violations shows it.", nil
	}
	var version int64 = -1
	for _, run := range runs {
		if run.RunID == runID {
			version = run.Version
		}
	}
	if version < 0 {
		return fmt.Sprintf("Alignment escalation was NOT resolved: run %s is not a run of this session.", runID), nil
	}
	e := app.CommandEnvelope{ProjectID: a.runtime.ProjectIdentity(), SessionID: a.sessionID, TargetID: target, ExpectedVersion: version, IdempotencyKey: uuid.NewString()}
	if _, err := a.runtime.CommandAlignmentDecision(a.localControl.Context(ctx), e, strings.ToLower(decision), reason); err != nil {
		return fmt.Sprintf("Alignment escalation was NOT resolved: %v", err), nil
	}
	return fmt.Sprintf("Decision %s recorded for %s. The violation itself stands; a scope change still needs a goal revision.", strings.ToLower(decision), target), nil
}
