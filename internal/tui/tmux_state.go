package tui

import (
	"context"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

func taskNativeWaiting(runs []execution.ExecutionRun, project, plan string, version int64, taskID string, boundRun ...string) bool {
	for _, run := range runs {
		if len(boundRun) > 0 && boundRun[0] != "" && run.RunID != boundRun[0] {
			continue
		}
		if string(run.ProjectID) != project || run.PlanID != plan || run.PlanVersion != version || run.State != execution.RunNeedsApproval {
			continue
		}
		for _, task := range run.Tasks {
			if task.TaskID == taskID && task.State == execution.TaskNeedsApproval && task.NativeTurn != nil {
				return true
			}
		}
	}
	return false
}
func (w *Workspace) monitorTaskState(a *activeTmuxAgent, h *driver.Handle) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-h.Done():
			record, err, _ := h.Outcome()
			state := "done"
			if err != nil || record.ExitCode != 0 {
				state = "failed"
			}
			w.tmuxMu.Lock()
			a.state = state
			taskID, runID, provider := a.taskID, a.runID, a.provider
			for key, value := range w.tmuxAlerts {
				if agentAlertKey(a, key) {
					if strings.HasSuffix(value, ": waiting") {
						w.tmuxAlerts[key] = strings.TrimSuffix(value, ": waiting") + ": " + state
					}
				}
			}
			w.tmuxMu.Unlock()
			_ = w.deliverEgressAlert(app.EgressAlert{RunID: runID, ParentRunID: runID, TaskID: taskID, Worker: provider, Kind: "worker " + state, State: state, Message: "Task " + taskID + " worker " + state + "."})
			return
		case <-ticker.C:
			m := w.marshalSession()
			m.mu.Lock()
			service, runID := m.service, m.runID
			m.mu.Unlock()
			w.tmuxMu.Lock()
			taskID, boundRun, executionRunID, canonicalTaskID := a.taskID, a.runID, a.executionRunID, a.canonicalTaskID
			w.tmuxMu.Unlock()
			if service == nil || runID == "" || runID != boundRun || w.runtime == nil {
				continue
			}
			run, err := service.Snapshot(context.Background(), runID)
			if err != nil {
				continue
			}
			runs, err := w.runtime.Execution().ListRuns(context.Background())
			if err != nil {
				continue
			}
			waiting := taskNativeWaiting(runs, string(service.CanonicalPlanProjectID()), run.PlanID, run.PlanVersion, taskID, executionRunID)
			for _, row := range w.runtime.EgressStatus() {
				if (row.TaskID == taskID || row.TaskID == canonicalTaskID) && (row.ParentRunID == runID || row.RunID == runID || row.ParentRunID == executionRunID) && len(row.Pending) > 0 {
					waiting = true
				}
			}
			w.tmuxMu.Lock()
			previous := a.state
			if !waiting && previous == "waiting" {
				a.state = "working"
				delete(w.tmuxDelivered, runID+":"+taskID+":task waiting:Task "+taskID+" is waiting for your input.")
				for key, value := range w.tmuxAlerts {
					if agentAlertKey(a, key) {
						if strings.HasSuffix(value, ": waiting") {
							w.tmuxAlerts[key] = strings.TrimSuffix(value, ": waiting") + ": working"
						}
					}
				}
			}
			w.tmuxMu.Unlock()
			if waiting && previous != "waiting" {
				_ = w.deliverEgressAlert(app.EgressAlert{RunID: runID, ParentRunID: runID, TaskID: taskID, Worker: a.provider, Kind: "task waiting", State: "waiting", Message: "Task " + taskID + " is waiting for your input."})
			} else if !waiting && previous == "waiting" {
				w.updateTmuxStatusLine(context.Background())
			}
		}
	}
}

// Replay durable incidents independently of whether their panes still exist.
func (w *Workspace) replayWorkerAlerts(ctx context.Context) {
	if w.runtime != nil {
		alerts, err := w.runtime.EgressNotifications(ctx)
		if err == nil {
			for _, alert := range alerts {
				_ = w.deliverEgressAlert(alert)
			}
		}
	}
	if w.store == nil {
		return
	}
	history, err := w.store.ListEvents(ctx)
	if err != nil {
		return
	}
	for _, event := range history {
		if event.Type == "HONEYPOT_HIT" {
			taskID, _ := event.Data["task_id"].(string)
			_ = w.deliverEgressAlert(app.EgressAlert{RunID: stringEventData(event.Data, "run_id"), ParentRunID: stringEventData(event.Data, "parent_run_id"), TaskID: taskID, Kind: "honeypot", State: "failed", Message: taskID + ": honeypot hit; task stopped; do not merge"})
		}
	}
}

// Caller holds tmuxMu; clear only the two identities owned by this agent.
func agentAlertKey(a *activeTmuxAgent, key string) bool {
	return (a.taskID != "" && key == a.runID+":"+a.taskID) || (a.canonicalTaskID != "" && key == a.executionRunID+":"+a.canonicalTaskID)
}
func stringEventData(data map[string]any, key string) string {
	value, _ := data[key].(string)
	return value
}
