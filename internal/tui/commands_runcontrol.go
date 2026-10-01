package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/google/uuid"
)

// handleRunControl pauses, resumes or cancels a run of this session through
// the authenticated operator boundary. Without "run:<id>" it acts only when
// exactly one run of the session can take the operation.
func (h *CommandHandler) handleRunControl(ctx context.Context, operation string, args []string) (string, error) {
	verb := strings.ToUpper(operation[:1]) + operation[1:]
	a, runs, err := h.sessionRuns(ctx)
	if err != nil {
		return "", fmt.Errorf("read runs: %w", err)
	}
	if a == nil || a.localControl == nil {
		return verb + " was NOT performed: authenticated runtime authorization is required.", nil
	}
	var target *execution.ExecutionRun
	if len(args) == 1 {
		id := strings.TrimPrefix(args[0], "run:")
		for i := range runs {
			if runs[i].RunID == id {
				target = &runs[i]
			}
		}
		if target == nil {
			return fmt.Sprintf("%s was NOT performed: run %s is not a run of this session.", verb, id), nil
		}
	} else {
		var candidates []execution.ExecutionRun
		for _, run := range runs {
			if runControllable(operation, run.State) {
				candidates = append(candidates, run)
			}
		}
		switch len(candidates) {
		case 0:
			return fmt.Sprintf("%s was NOT performed: no run of this session can be %sd.", verb, strings.TrimSuffix(operation, "e")), nil
		case 1:
			target = &candidates[0]
		default:
			var ids []string
			for _, c := range candidates {
				ids = append(ids, "run:"+c.RunID+" ("+string(c.State)+")")
			}
			return fmt.Sprintf("%s was NOT performed: more than one run qualifies; name one: %s", verb, strings.Join(ids, ", ")), nil
		}
	}
	e := app.CommandEnvelope{ProjectID: a.runtime.ProjectIdentity(), SessionID: a.sessionID, TargetID: "run:" + target.RunID,
		ExpectedVersion: target.Version, IdempotencyKey: uuid.NewString()}
	result, err := a.runtime.CommandRunControl(a.localControl.Context(ctx), e, operation)
	if err != nil {
		return fmt.Sprintf("%s was NOT performed: %v", verb, err), nil
	}
	state := string(result.Run.State)
	switch {
	case operation == "resume":
		return fmt.Sprintf("Run %s resumed (%s); canonical execution restarted after the goal binding was re-validated.", target.RunID, state), nil
	case result.Executing:
		return fmt.Sprintf("Run %s %s, still settling: no new task is dispatched, and a task already in a provider turn finishes or is cancelled first.", target.RunID, state), nil
	default:
		return fmt.Sprintf("Run %s %s; no task is executing.", target.RunID, state), nil
	}
}

func runControllable(operation string, state execution.RunState) bool {
	switch operation {
	case "pause":
		return state == execution.RunRunning || state == execution.RunNeedsApproval
	case "resume":
		return state == execution.RunPaused
	default:
		return !state.IsTerminal()
	}
}
