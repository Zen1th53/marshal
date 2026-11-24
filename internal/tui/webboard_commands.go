package tui

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/webboard"
)

// webWorkCapabilities reads canonical tasks rather than the browser's selection.
func (w *Workspace) webWorkCapabilities(ctx context.Context) []webboard.SettingCapability {
	if w.runtime == nil || w.controlSource() == nil {
		return nil
	}
	a, ok := w.controlSource().Authority.(*runtimeControlAuthority)
	if !ok || a == nil || a.localControl == nil {
		return nil
	}
	m := w.marshalSession()
	m.mu.Lock()
	runID, service := m.runID, m.service
	m.mu.Unlock()
	if service == nil {
		service = w.runtime.Marshal()
	}
	if service == nil || runID == "" {
		return nil
	}
	record, err := service.Store.GetMarshalRun(ctx, service.ProjectID, runID)
	if err != nil || record.Value.CommandFence != "" || record.Value.Operation != nil {
		return nil
	}
	var caps []webboard.SettingCapability
	for _, task := range record.Value.Tasks {
		var ops []string
		switch task.State {
		case marshal.Returned:
			ops = []string{"retry", "retry.debug"}
		case marshal.Escalated:
			ops = []string{"retry", "retry.debug", "reassign", "model.set", "skip"}
		case marshal.Reassigned:
			ops = []string{"reassign", "model.set"}
		case marshal.Queued:
			if app.OperatorCanMoveQueuedTask() {
				ops = []string{"model.set"}
			}
		case marshal.HandedIn:
			ops = []string{"accept", "return"}
		}
		for _, op := range ops {
			caps = append(caps, webboard.SettingCapability{Op: "task/" + op, Target: webboard.Target{Kind: "task", ID: task.PlanTaskID}, Revision: record.Revision, RunID: runID, AttemptID: app.TaskCommandDigest(task)})
		}
	}

	views, _, _ := w.runtime.ApprovalProjection(ctx, runID, record.Value, false)
	for _, v := range views {
		if v.Status != "REQUESTED" {
			continue
		}
		for _, task := range record.Value.Tasks {
			if task.PlanTaskID == v.TaskID {
				caps = append(caps, webboard.SettingCapability{Op: "task/approve-task", Target: webboard.Target{Kind: "task", ID: task.PlanTaskID}, Revision: record.Revision, RunID: runID, AttemptID: app.TaskCommandDigest(task), ApprovalID: v.ApprovalID})
			}
		}
	}
	caps = append(caps, w.webLaneCapabilities(record.Value, runID, record.Revision)...)
	return caps
}

// webWorkCommand enters the existing writer under current local authority. The
// admitted digest survives the input queue and is checked again by that writer.
func (w *Workspace) webWorkCommand(a webboard.Action) webboard.Result {
	if a.Command == nil || a.Commit == nil {
		return refused("Work authority is unavailable.")
	}
	i := *a.Command
	var payload webboard.SettingPayload
	if json.Unmarshal([]byte(i.Value), &payload) != nil {
		return refused("Invalid work payload.")
	}
	var execute func() webboard.Result
	result := a.Commit(func() webboard.Result {
		if w.runtime == nil || w.store == nil {
			return refused("Work authority is unavailable.")
		}
		w.mu.RLock()
		current := w.ultraProjectLocked() == i.ProjectID && webboard.SettingsEpoch(w.webSettingsFenceLocked()) == i.ProjectEpoch
		w.mu.RUnlock()
		if !current {
			return refused("The project or session changed.")
		}
		m := w.marshalSession()
		m.mu.Lock()
		runID := m.runID
		m.mu.Unlock()
		if runID != i.RunID {
			return refused("The selected run changed.")
		}
		m.mu.Lock()
		service := m.service
		m.mu.Unlock()
		if service == nil {
			service = w.runtime.Marshal()
		}
		if service == nil {
			return refused("The run writer is unavailable.")
		}
		a, ok := w.controlSource().Authority.(*runtimeControlAuthority)
		if !ok || a == nil || a.localControl == nil {
			return refused("Local authority is unavailable.")
		}
		if strings.HasPrefix(i.Op, "lane/") {
			return w.webLaneCommand(i, payload, service, a)
		}
		if i.Op == "task/approve-task" {
			return w.webApproveTask(i, payload, service)
		}
		input, err := webRunInput(i, payload)
		if err != nil {
			return refused(err.Error())
		}
		if err = w.store.StartWebRequest(context.Background(), i, i.RunID); err != nil {
			return refused("The work journal could not be recorded.")
		}
		st := w.store
		execute = func() webboard.Result {
			receipt, err := service.CommandRun(a.localControl.Context(context.Background()), app.CommandEnvelope{ProjectID: a.runtime.ProjectIdentity(), SessionID: i.RunID, TargetID: i.Key, ExpectedVersion: i.Revision, IdempotencyKey: i.RequestID}, input)
			r := webboard.Result{Status: "done", Text: "Run action " + receipt.Operation + ": " + receipt.State + "."}
			status := "applied"
			if err != nil {
				r = refusedErr(err)
				status = "refused"
			} else if receipt.State == "refused" || receipt.State == "held" {
				r = refused(receipt.Detail)
				status = "refused"
			} else if receipt.State == "queued" {
				r.Status = "started"
				status = "applying"
			}
			_ = st.FinishWebRequest(context.Background(), i, store.WebReceipt{RequestID: i.RequestID, Status: status, Code: receipt.State, Revision: i.Revision, Effects: []string{"run-command"}, Result: r.Text})
			w.requestRepaint()
			return r
		}
		return webboard.Result{Status: "started"}
	})
	if execute != nil {
		return execute()
	}
	return result
}

func webRunInput(i store.WebIntent, p webboard.SettingPayload) (app.RunCommandInput, error) {
	input := app.RunCommandInput{TaskID: i.Key, TaskDigest: i.AttemptID, Worker: p.Provider, Reason: p.Reason}
	switch i.Op {
	case "task/retry", "task/retry.debug":
		input.Operation = "retry"
		input.FreshSession = true
	case "task/reassign", "task/model.set":
		input = taskModelCommand(i.Key, p.Provider, p.Reason, i.AttemptID)
	case "task/skip":
		input.Operation = "cancel"
	case "task/accept":
		input.Operation = "accept"
	case "task/return":
		input.Operation = "return"
	default:
		return input, errors.New("This work operation is unavailable.")
	}
	return input, nil
}

// reconcileWebWork observes the run journal without executing a second effect.
func (w *Workspace) reconcileWebWork(ctx context.Context) {
	if w.runtime == nil || w.store == nil || w.runtime.Marshal() == nil {
		return
	}
	w.mu.RLock()
	project := w.ultraProjectLocked()
	w.mu.RUnlock()
	intents, err := w.store.UnfinishedWebRequests(ctx, project)
	if err != nil {
		return
	}
	service := w.runtime.Marshal()
	for _, i := range intents {
		if !strings.HasPrefix(i.Op, "task/") && !strings.HasPrefix(i.Op, "lane/") {
			continue
		}
		if i.Op == "task/approve-task" {
			approval, readErr := w.runtime.Execution().Engine().ApprovalManager().GetApproval(i.RequestBinding)
			key := fmt.Sprintf("%x", sha256.Sum256([]byte(i.RequestID)))
			if readErr == nil && approval.TaskID == i.Key && approval.DecisionCommandKey == key && (approval.Status == "APPROVED" || approval.Status == "CONSUMED") {
				_ = w.store.FinishWebRequest(ctx, i, store.WebReceipt{RequestID: i.RequestID, Status: "applied", Code: "applied", Revision: i.Revision, Effects: []string{"task-approval"}, Result: "The bound task approval was committed."})
			}
			continue
		}
		run, err := service.Snapshot(ctx, i.RunID)
		if err != nil {
			continue
		}
		for _, c := range run.Commands {
			if c.ID != i.RequestID || c.State == "queued" || c.State == "applying" {
				continue
			}
			status := "applied"
			if c.State != "applied" {
				status = "refused"
			}
			_ = w.store.FinishWebRequest(ctx, i, store.WebReceipt{RequestID: i.RequestID, Status: status, Code: c.State, Revision: i.Revision, Effects: []string{"run-command"}, Result: c.Detail})
		}
	}
}

// webApproveTask uses the same approval writer as the explicit terminal action.
func (w *Workspace) webApproveTask(i store.WebIntent, p webboard.SettingPayload, service *app.MarshalService) webboard.Result {
	run, err := service.Snapshot(context.Background(), i.RunID)
	if err != nil {
		return refusedErr(err)
	}
	found := false
	for _, task := range run.Tasks {
		if task.PlanTaskID == i.Key && app.TaskCommandDigest(task) == i.AttemptID {
			found = true
		}
	}
	if !found {
		return refused("The task attempt changed.")
	}
	views, _, err := w.runtime.ApprovalProjection(context.Background(), i.RunID, run, false)
	bound := false
	if err == nil {
		for _, v := range views {
			if v.TaskID == i.Key && v.ApprovalID == p.ApprovalID && v.Status == "REQUESTED" {
				bound = true
			}
		}
	}
	if !bound {
		return refused("The selected approval changed.")
	}
	if err = w.store.StartWebRequest(context.Background(), i, p.ApprovalID); err != nil {
		return refused("The approval journal could not be recorded.")
	}
	text, err := w.approveProcess05Task(context.Background(), p.ApprovalID, i.RequestID)
	status := "applied"
	result := webboard.Result{Status: "done", Text: text}
	if err != nil {
		status = "refused"
		result = refusedErr(err)
	}
	_ = w.store.FinishWebRequest(context.Background(), i, store.WebReceipt{RequestID: i.RequestID, Status: status, Code: status, Revision: i.Revision, Effects: []string{"task-approval"}, Result: result.Text})
	return result
}
