package tui

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/processgroup"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tmux"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// Run-wide browser controls. Each advertised op names its writer here:
//
//	run/pause, run/stop   the run writer's priority signal (CommandRun)
//	run/resume            marshalResume, the same resume the terminal runs
//	run/stop-workers      the bound run's workers, each process fenced by
//	                      its own pane, PID and supervisor identity
//	run/amend.deny        marshalAmendDeny, the terminal's denial
//
// Pause, stop and stop-workers bind the approved plan identity (the run
// revision keeps moving while workers run); resume and the amendment denial
// also bind the run revision. A changed plan, a pending amendment, a hold or
// an unfinished operation stops the op from being advertised, and the same
// check runs again when the admitted op leaves the queue.
var webRunOps = []string{"run/pause", "run/stop", "run/resume", "run/stop-workers", "run/amend.deny"}

func webRunOp(op string) bool { return slices.Contains(webRunOps, op) }

// webWorkOp is any op entering a run writer rather than the settings writer.
func webWorkOp(op string) bool {
	return strings.HasPrefix(op, "task/") || strings.HasPrefix(op, "lane/") || strings.HasPrefix(op, "run/")
}

func webRunBinding(run marshal.Run, extra string) string {
	raw, _ := json.Marshal(struct {
		Plan    string
		Version int64
		Scope   string
		Extra   string
	}{run.PlanID, run.PlanVersion, run.ApprovalScopeDigest, extra})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// webRunLanes are the bound run's worker processes with a full identity,
// sorted so their binding is stable.
func (w *Workspace) webRunLanes(runID string) []*activeTmuxAgent {
	w.tmuxMu.Lock()
	defer w.tmuxMu.Unlock()
	var out []*activeTmuxAgent
	for _, a := range w.tmuxActiveWins {
		if a.role == "task" && a.runID == runID && a.paneID != "" && a.paneID != w.tmuxMarshalPaneID && a.pid > 0 && a.supervisor.Start != "" {
			out = append(out, copyAgentLocked(a))
		}
	}
	slices.SortFunc(out, func(a, b *activeTmuxAgent) int { return strings.Compare(a.id, b.id) })
	return out
}

func webLanesBinding(run marshal.Run, lanes []*activeTmuxAgent) string {
	var parts []string
	for _, a := range lanes {
		task := marshal.Task{PlanTaskID: a.taskID}
		for _, t := range run.Tasks {
			if t.PlanTaskID == a.taskID {
				task = t
			}
		}
		parts = append(parts, a.id+"="+webLaneBinding(task, a))
	}
	return strings.Join(parts, ",")
}

// webRunCapabilities lists the run-wide ops the current run takes now.
func (w *Workspace) webRunCapabilities(service *app.MarshalService, run marshal.Run, runID string, revision int64) []webboard.SettingCapability {
	m := w.marshalSession()
	m.mu.Lock()
	pending, amended := m.pending != nil, m.amended
	m.mu.Unlock()
	target := webboard.Target{Kind: "run", ID: runID}
	add := func(caps []webboard.SettingCapability, op string, rev int64, binding string) []webboard.SettingCapability {
		return append(caps, webboard.SettingCapability{Op: op, Target: target, RunID: runID, Revision: rev, AttemptID: binding})
	}
	var caps []webboard.SettingCapability
	if run.State == marshal.Closed || run.State == marshal.Drafting {
		return nil
	}
	if run.State != marshal.AwaitingUser && run.CommandFence == "" {
		caps = add(caps, "run/pause", 0, webRunBinding(run, ""))
		caps = add(caps, "run/stop", 0, webRunBinding(run, ""))
	}
	if lanes := w.webRunLanes(runID); len(lanes) > 0 {
		caps = add(caps, "run/stop-workers", 0, webRunBinding(run, webLanesBinding(run, lanes)))
	}
	// Resume only what an owner's resume moves on: no amendment to decide,
	// no Board hold, no writer already driving the run.
	if run.State == marshal.AwaitingUser && run.Pause != nil && run.Pause.Action == marshal.PauseResume && run.Operation == nil && !pending && !m.dispatchHeld() && service.ActiveExecution(runID) == "" {
		caps = add(caps, "run/resume", revision, webRunBinding(run, "resume"))
	}
	if amended {
		caps = add(caps, "run/amend.deny", revision, webRunBinding(run, "amend"))
	}
	return caps
}

// webRunCommand rechecks the admitted binding against the run now, records
// the start in the journal and returns the effect to run after the commit.
func (w *Workspace) webRunCommand(i store.WebIntent, p webboard.SettingPayload, service *app.MarshalService, authority *runtimeControlAuthority) (func() webboard.Result, webboard.Result) {
	record, err := service.Store.GetMarshalRun(context.Background(), service.ProjectID, i.RunID)
	if err != nil {
		return nil, refusedErr(err)
	}
	bound := false
	for _, c := range w.webRunCapabilities(service, record.Value, i.RunID, record.Revision) {
		if c.Op == i.Op && c.AttemptID == i.AttemptID && c.Revision == i.Revision {
			bound = true
		}
	}
	if !bound {
		return nil, refused("The run, its approved plan or its workers changed; refresh and try again.")
	}
	if err = w.store.StartWebRequest(context.Background(), i, i.AttemptID); err != nil {
		return nil, refused("The run action journal could not be recorded.")
	}
	finish := func(status, code string, effects []string, r webboard.Result) webboard.Result {
		_ = w.store.FinishWebRequest(context.Background(), i, store.WebReceipt{RequestID: i.RequestID, Status: status, Code: code, Revision: i.Revision, Effects: effects, Result: r.Text})
		w.requestRepaint()
		return r
	}
	return func() webboard.Result {
		ctx := context.Background()
		switch i.Op {
		case "run/pause", "run/stop":
			op := strings.TrimPrefix(i.Op, "run/")
			active := service.ActiveExecution(i.RunID) != ""
			receipt, err := service.CommandRun(authority.localControl.Context(ctx), app.CommandEnvelope{ProjectID: authority.runtime.ProjectIdentity(), SessionID: i.RunID, ExpectedVersion: i.Revision, IdempotencyKey: i.RequestID}, app.RunCommandInput{Operation: op, Reason: p.Reason})
			if err != nil {
				return finish("refused", "refused", []string{}, refusedErr(err))
			}
			if receipt.State == "refused" || receipt.State == "held" {
				return finish("refused", receipt.State, []string{}, refused(receipt.Detail))
			}
			if op == "stop" && !active {
				// No writer drives the run: the Marshal step in flight is
				// cancelled as the terminal's stop does; its state is kept.
				w.marshalStop()
			}
			if receipt.State == "queued" {
				// The active writer drains it at its next safe point; the
				// journal reconciliation ends this receipt from its outcome.
				w.requestRepaint()
				return webboard.Result{Status: "started", Text: "Run " + op + " queued for the active run writer; the receipt follows its outcome."}
			}
			return finish("applied", receipt.State, []string{"run-command"}, webboard.Result{Status: "done", Text: "Run " + op + ": " + receipt.State + "; new starts are fenced."})
		case "run/resume":
			text, err := w.marshalResume(ctx)
			if err != nil {
				return finish("refused", "refused", []string{}, refusedErr(err))
			}
			return finish("applied", "resume_started", []string{"run-resume"}, webboard.Result{Status: "done", Text: text})
		case "run/amend.deny":
			text, err := w.marshalAmendDeny()
			if err != nil {
				return finish("refused", "refused", []string{}, refusedErr(err))
			}
			return finish("applied", "applied", []string{"amendment-denied"}, webboard.Result{Status: "done", Text: text})
		case "run/stop-workers":
			effects, ok := w.webStopRunWorkers(ctx, i.RunID)
			if !ok {
				return finish("interrupted", "outcome_unknown", effects, refused("Some worker exits could not be proven: "+strings.Join(effects, ", ")+"."))
			}
			return finish("applied", "applied", effects, webboard.Result{Status: "done", Text: "Stopped the run's workers: " + strings.Join(effects, ", ") + "."})
		}
		return finish("refused", "refused", []string{}, refused("This run operation is unavailable."))
	}, webboard.Result{Status: "started"}
}

// webStopRunWorkers stops each worker of the run whose pane, PID and
// supervisor are still the ones bound, and records each verified exit or an
// unknown outcome. A worker whose identity changed is left alone.
func (w *Workspace) webStopRunWorkers(ctx context.Context, runID string) ([]string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	w.tmuxMu.Lock()
	session := w.tmuxSession
	w.tmuxMu.Unlock()
	panes, listErr := tmux.ListPanes(ctx, session)
	var effects []string
	ok := true
	for _, a := range w.webRunLanes(runID) {
		exact := false
		for _, pane := range panes {
			if pane.PaneID == a.paneID && pane.PID == a.pid {
				exact = true
			}
		}
		alive, err := processgroup.RunningReference(a.supervisor)
		switch {
		case err == nil && !alive:
			effects = append(effects, a.id+":exited")
			continue
		case listErr != nil || err != nil || !exact:
			effects = append(effects, a.id+":unknown")
			ok = false
			continue
		}
		// StopReference verifies the kernel start time and waits for the
		// supervisor's cleanup before the exit is checked.
		if err = processgroup.StopReference(a.supervisor); err == nil {
			alive, err = processgroup.RunningReference(a.supervisor)
		}
		if err == nil && !alive && a.doneChan != nil {
			select {
			case <-a.doneChan:
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
		if err != nil || alive {
			effects = append(effects, a.id+":unknown")
			ok = false
			continue
		}
		effects = append(effects, a.id+":exited")
	}
	if len(effects) == 0 {
		effects = []string{"none"}
	}
	return effects, ok
}
