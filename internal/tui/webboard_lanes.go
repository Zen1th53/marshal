package tui

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/processgroup"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tmux"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// webLaneBinding includes both the task attempt and immutable process identity.
func webLaneBinding(task marshal.Task, a *activeTmuxAgent) string {
	raw, _ := json.Marshal(struct {
		Task, Pane string
		PID        int
		Supervisor processgroup.Reference
	}{app.TaskCommandDigest(task), a.paneID, a.pid, a.supervisor})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func (w *Workspace) webLaneCapabilities(run marshal.Run, runID string, revision int64) []webboard.SettingCapability {
	w.tmuxMu.Lock()
	defer w.tmuxMu.Unlock()
	var caps []webboard.SettingCapability
	for _, a := range w.tmuxActiveWins {
		if a.role != "task" || a.runID != runID || a.paneID == "" || a.paneID == w.tmuxMarshalPaneID || a.pid <= 0 || a.supervisor.Start == "" || a.state != "working" {
			continue
		}
		for _, task := range run.Tasks {
			if task.PlanTaskID != a.taskID {
				continue
			}
			ops := []string{"lane/restart"}
			// A relay or result pane cannot receive provider input.
			if !a.readOnly {
				ops = append(ops, "lane/nudge")
			}
			for _, op := range ops {
				caps = append(caps, webboard.SettingCapability{TaskID: task.PlanTaskID, Op: op, Target: webboard.Target{Kind: "lane", ID: a.id}, RunID: runID, Revision: revision, AttemptID: webLaneBinding(task, a)})
			}
		}
	}
	return caps
}

// webBoundLane refuses a reused pane, a changed attempt or the chat process.
func (w *Workspace) webBoundLane(i store.WebIntent, run marshal.Run) (*activeTmuxAgent, string, error) {
	w.tmuxMu.Lock()
	defer w.tmuxMu.Unlock()
	a := w.tmuxActiveWins[i.Key]
	if a == nil || a.role != "task" || a.runID != i.RunID || a.paneID == "" || a.paneID == w.tmuxMarshalPaneID || a.pid <= 0 || a.supervisor.Start == "" || a.state != "working" {
		return nil, "", fmt.Errorf("The worker identity changed.")
	}
	for _, task := range run.Tasks {
		if task.PlanTaskID == a.taskID && webLaneBinding(task, a) == i.AttemptID {
			return copyAgentLocked(a), a.taskID, nil
		}
	}
	return nil, "", fmt.Errorf("The worker task attempt changed.")
}

func (w *Workspace) webLaneCommand(i store.WebIntent, _ webboard.SettingPayload, service *app.MarshalService, authority *runtimeControlAuthority) webboard.Result {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	run, err := service.Snapshot(ctx, i.RunID)
	if err != nil {
		return refusedErr(err)
	}
	a, taskID, err := w.webBoundLane(i, run)
	if err != nil {
		return refusedErr(err)
	}
	w.tmuxMu.Lock()
	session := w.tmuxSession
	w.tmuxMu.Unlock()
	panes, err := tmux.ListPanes(ctx, session)
	if err != nil {
		return refusedErr(err)
	}
	exact := false
	for _, pane := range panes {
		if pane.PaneID == a.paneID && pane.PID == a.pid {
			exact = true
		}
	}
	alive, err := processgroup.RunningReference(a.supervisor)
	if err != nil || !alive || !exact {
		return refused("The exact worker process cannot be verified.")
	}
	if i.Op == "lane/nudge" && a.readOnly {
		return refused("This worker does not accept status requests.")
	}
	latest, loadErr := service.Snapshot(ctx, i.RunID)
	if loadErr != nil {
		return refusedErr(loadErr)
	}
	if _, _, checkErr := w.webBoundLane(i, latest); checkErr != nil {
		return refusedErr(checkErr)
	}
	original := run
	if err = w.store.StartWebRequest(ctx, i, i.AttemptID); err != nil {
		return refused("The pane action journal could not be recorded.")
	}
	if i.Op == "lane/nudge" {
		_, err = tmux.RunCommand(ctx, "send-keys", "-t", a.paneID, "-l", "Please report your current task status and any blocker. Do not change scope.")
		if err == nil {
			_, err = tmux.RunCommand(ctx, "send-keys", "-t", a.paneID, "Enter")
		}
	} else {
		// StopReference verifies kernel start time and waits for supervisor cleanup.
		err = processgroup.StopReference(a.supervisor)
		if err == nil {
			alive, err = processgroup.RunningReference(a.supervisor)
			if alive {
				err = fmt.Errorf("Worker exit is not proven.")
			}
		}
		if err == nil && a.doneChan != nil {
			select {
			case <-a.doneChan:
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
		if err == nil {
			// Collection follows process exit on the run writer. Wait for that
			// exact attempt to settle before asking the writer to retry it.
			for {
				run, err = service.Snapshot(ctx, i.RunID)
				settling := false
				if err == nil {
					for _, task := range run.Tasks {
						if task.PlanTaskID == taskID && task.State == marshal.Dispatched {
							settling = true
						}
					}
				}
				if err != nil || !settling {
					break
				}
				select {
				case <-ctx.Done():
					err = ctx.Err()
				case <-time.After(20 * time.Millisecond):
				}
				if err != nil {
					break
				}
			}
			if err == nil {
				if run.PlanID != original.PlanID || run.PlanVersion != original.PlanVersion || run.ApprovalScopeDigest != original.ApprovalScopeDigest {
					err = fmt.Errorf("The approved plan changed while the worker stopped.")
				}
				var task *marshal.Task
				for n := range run.Tasks {
					if run.Tasks[n].PlanTaskID == taskID {
						task = &run.Tasks[n]
					}
				}
				if err == nil && (task == nil || task.Worker != a.provider && a.provider != "" || (task.State != marshal.Escalated && task.State != marshal.Returned)) {
					err = fmt.Errorf("Worker stopped; retry awaits task reconciliation.")
				} else if err == nil {
					receipt, applyErr := service.CommandRun(authority.localControl.Context(ctx), app.CommandEnvelope{ProjectID: authority.runtime.ProjectIdentity(), SessionID: i.RunID, TargetID: taskID, ExpectedVersion: i.Revision, IdempotencyKey: i.RequestID}, app.RunCommandInput{Operation: "retry", TaskID: taskID, TaskDigest: app.TaskCommandDigest(*task), FreshSession: true})
					err = applyErr
					if err == nil && receipt.State != "applied" && receipt.State != "queued" {
						err = fmt.Errorf("Retry %s: %s", receipt.State, receipt.Detail)
					}
				}
			}
		}
	}
	status := "applied"
	result := webboard.Result{Status: "done", Text: strings.TrimPrefix(i.Op, "lane/") + " applied to the selected worker."}
	if err != nil {
		status = "interrupted"
		result = refusedErr(err)
	}
	_ = w.store.FinishWebRequest(context.Background(), i, store.WebReceipt{RequestID: i.RequestID, Status: status, Code: status, Revision: i.Revision, Effects: []string{"pane-action"}, Result: result.Text})
	return result
}
