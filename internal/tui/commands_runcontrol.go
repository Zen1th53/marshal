package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
		return msgNothingChanged, nil
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
			return fmt.Sprintf("%s was NOT performed: no run of this session can be %s.", verb, map[string]string{"pause": "paused", "resume": "resumed", "cancel": "cancelled"}[operation]), nil
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
	switch operation {
	case "pause":
		// Any paused run of the session holds the Marshal's dispatch, so it
		// does not start the paused work again behind the person's back.
		h.holdMarshalDispatch()
	case "resume":
		// Once no run of the session is paused, the hold is released.
		h.ws.noteRunControl(ctx)
		if h.ws.runResumable.Load() == 0 {
			h.ws.marshalSession().releaseDispatch()
		}
	}
	switch {
	case operation == "resume":
		return fmt.Sprintf("Run %s resumed (%s); canonical execution restarted after the goal binding was re-validated.", target.RunID, state), nil
	case result.Executing:
		return fmt.Sprintf("Run %s %s, still settling: no new task is dispatched, and a task already in a provider turn finishes or is cancelled first.", target.RunID, state), nil
	default:
		return fmt.Sprintf("Run %s %s; no task is executing.", target.RunID, state), nil
	}
}

// holdMarshalDispatch holds the current Marshal run's dispatch (none when no
// Marshal run is open in this session).
func (h *CommandHandler) holdMarshalDispatch() {
	m := h.ws.marshalSession()
	m.mu.Lock()
	runID := m.runID
	m.mu.Unlock()
	if runID != "" {
		m.holdDispatch(runID)
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

// noteRunControl counts this session's runs that pause and resume can take
// now, for the palette's "pause the run" and "resume the run" rows. It reads
// the runs, so it runs where the store is read already (RefreshState, the
// Board's follow), never on the paint path.
func (w *Workspace) noteRunControl(ctx context.Context) {
	if w.cmd == nil {
		return
	}
	_, runs, err := w.cmd.sessionRuns(ctx)
	if err != nil {
		return
	}
	m := w.marshalSession()
	m.mu.Lock()
	open := m.runID != ""
	m.mu.Unlock()
	pause, resume := runControlCounts(runs, waitingForMarshal(open, m.dispatchHeld()))
	w.runPausable.Store(pause)
	w.runResumable.Store(resume)
}

// runControlCounts is how many of runs /pause and /resume can each take; a
// run waiting reports is already resumed and only waits for the Marshal to
// continue it.
func runControlCounts(runs []execution.ExecutionRun, waiting func(execution.ExecutionRun) bool) (pause, resume int32) {
	for _, run := range runs {
		if runControllable("pause", run.State) {
			pause++
		}
		if runControllable("resume", run.State) && !waiting(run) {
			resume++
		}
	}
	return pause, resume
}

// waitingForMarshal reports, for a paused run, whether it is already resumed
// as far as the person is concerned: a Marshal run is open, its dispatch is
// no longer held, and the run is a lane the Marshal continues itself
// (marshalContinues) on its next dispatch.
func waitingForMarshal(marshalOpen, held bool) func(execution.ExecutionRun) bool {
	return func(run execution.ExecutionRun) bool {
		return marshalOpen && !held && marshalContinues(run)
	}
}

// handleRunControlAll pauses every run of this session that can be paused, or
// resumes every paused one: the palette's "pause the run" and "resume the
// run" (/pause all, /resume all). Each run goes through the same per-run
// control as /pause run:<id>, at the version it has just before; one that
// fails is named and the rest stay done. Pausing also holds the Marshal's
// dispatch, so it opens no new task until the run is resumed.
func (h *CommandHandler) handleRunControlAll(ctx context.Context, operation string) (string, error) {
	a, runs, err := h.sessionRuns(ctx)
	if err != nil {
		return "", fmt.Errorf("read runs: %w", err)
	}
	if a == nil || a.localControl == nil {
		return msgNothingChanged, nil
	}
	service, err := a.execution()
	if err != nil {
		return msgNothingChanged, nil
	}
	m := h.ws.marshalSession()
	m.mu.Lock()
	marshalRun := m.runID
	m.mu.Unlock()
	waiting := waitingForMarshal(marshalRun != "", m.dispatchHeld())
	var targets []execution.ExecutionRun
	for _, run := range runs {
		if runControllable(operation, run.State) && !(operation == "resume" && waiting(run)) {
			targets = append(targets, run)
		}
	}
	if len(targets) == 0 {
		if operation == "pause" {
			return "Nothing is running to pause.", nil
		}
		m.releaseDispatch()
		return "Nothing is paused to resume.", nil
	}
	// A lane parent first: pausing it pauses its running lanes with it.
	slices.SortStableFunc(targets, func(x, y execution.ExecutionRun) int {
		return min(1, len(y.TaskLanes)) - min(1, len(x.TaskLanes))
	})
	failed := map[string]error{}
	continued := map[string]bool{}
	for _, t := range targets {
		if operation == "resume" && marshalRun != "" && marshalContinues(t) {
			// The Marshal continues this lane itself once its hold is
			// released, under its own dispatch, so it collects the result.
			continued[t.RunID] = true
			continue
		}
		current, err := service.Engine().GetRun(ctx, t.RunID)
		if err != nil {
			failed[t.RunID] = err
			continue
		}
		if !runControllable(operation, current.State) {
			continue // its parent's control already took it
		}
		e := app.CommandEnvelope{ProjectID: a.runtime.ProjectIdentity(), SessionID: a.sessionID, TargetID: "run:" + current.RunID,
			ExpectedVersion: current.Version, IdempotencyKey: uuid.NewString()}
		if _, err := runControlEach(a.localControl.Context(ctx), a.runtime, e, operation); err != nil {
			failed[t.RunID] = err
		}
	}
	_, after, _ := h.sessionRuns(ctx)
	state := map[string]execution.RunState{}
	for _, run := range after {
		state[run.RunID] = run.State
	}
	done, total := 0, 0
	var notDone []string
	for _, t := range targets {
		if len(t.TaskLanes) > 0 && t.ParentRunID == "" {
			// A lane parent is the run's summary, not a task.
			if err := failed[t.RunID]; err != nil && runControllable(operation, state[t.RunID]) {
				notDone = append(notDone, "one part of the run "+runControlFailure(operation, err))
			}
			continue
		}
		total++
		if !continued[t.RunID] && runControllable(operation, state[t.RunID]) {
			notDone = append(notDone, runControlName(t)+" "+runControlFailure(operation, failed[t.RunID]))
			continue
		}
		done++
	}
	if operation == "pause" {
		if marshalRun != "" && (done > 0 || total == 0) {
			m.holdDispatch(marshalRun)
		}
	} else {
		m.releaseDispatch()
	}
	h.ws.noteRunControl(ctx)
	verb, noun := "Paused", "running task"
	if operation == "resume" {
		verb, noun = "Resumed", "task"
	}
	line := fmt.Sprintf("%s %d %s.", verb, done, plural(done, noun, noun+"s"))
	if total == 0 {
		line = verb + " the run."
	}
	if len(notDone) > 0 {
		line = strings.TrimSuffix(line, ".") + "; " + strings.Join(notDone, "; ") + "."
	}
	return line, nil
}

// runControlEach is the per-run control handleRunControlAll applies to each
// run: the runtime's own CommandRunControl, the one /pause run:<id> uses.
// Tests replace it to fail one run or to keep resume from restarting
// execution.
var runControlEach = func(ctx context.Context, r *app.Runtime, e app.CommandEnvelope, operation string) (execution.RunControlResult, error) {
	return r.CommandRunControl(ctx, e, operation)
}

// runControlName names a run as the person knows it: its task for a task
// lane, "one part of the run" otherwise. It never shows a run id.
func runControlName(run execution.ExecutionRun) string {
	if run.LaneTaskID != "" {
		return "task " + run.LaneTaskID
	}
	return "one part of the run"
}

// marshalContinues reports whether the Marshal continues a paused run itself
// on resume: a task lane whose task has not finished. Its dispatch reopens
// the same lane and runs it under the Marshal's own collection; resuming it
// here would restart it outside that.
func marshalContinues(run execution.ExecutionRun) bool {
	if run.ParentRunID == "" || run.LaneTaskID == "" {
		return false
	}
	state := run.Tasks[run.LaneTaskID].State
	return state != execution.TaskCompletedPendingVerify && !state.IsTerminal()
}

// runControlFailure says in plain words why a run was not paused or resumed.
func runControlFailure(operation string, err error) string {
	not := "could not be paused"
	if operation == "resume" {
		not = "could not be resumed"
	}
	switch {
	case err == nil:
		return not
	case errors.Is(err, execution.ErrRunConflict):
		return not + " (it changed meanwhile; try again)"
	}
	return not + " (" + err.Error() + ")"
}
