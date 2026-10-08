package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/worker"
)

func (r *Runtime) armHoneypot(worktree string) (*worker.Honeypot, error) {
	r.honeypotMu.Lock()
	defer r.honeypotMu.Unlock()
	if trap := r.honeypots[worktree]; trap != nil {
		return trap, nil
	}
	trap, err := worker.NewHoneypot(worktree)
	if err != nil {
		return nil, fmt.Errorf("arm honeypot: %w", err)
	}
	if r.honeypots == nil {
		r.honeypots = make(map[string]*worker.Honeypot)
	}
	r.honeypots[worktree] = trap
	return trap, nil
}

func (r *Runtime) honeypotStatus() string {
	r.honeypotMu.Lock()
	defer r.honeypotMu.Unlock()
	if len(r.honeypots) == 0 {
		return "ready (arms on governed dispatch)"
	}
	return "armed"
}

func (r *Runtime) checkHoneypot(ctx context.Context, taskID string, trap *worker.Honeypot, stdout, stderr []byte, identity ...EgressAlert) error {
	if trap == nil {
		return errors.New("honeypot: required scan identity unavailable")
	}
	err := trap.Check(ctx, stdout, stderr)
	scanErr := r.recordHoneypotScan(context.WithoutCancel(ctx), taskID, trap, err)
	if scanErr != nil {
		return errors.Join(err, scanErr)
	}
	if !errors.Is(err, worker.ErrHoneypot) {
		return err
	}
	alert := EgressAlert{TaskID: taskID}
	if len(identity) > 0 {
		alert = identity[0]
	}
	alert.Kind, alert.State, alert.Message = "honeypot", "failed", taskID+": honeypot hit; task stopped; do not merge"
	// The durable alert is visible in the operator's event feed. The returned
	// failure also reaches the Marshal instead of an accepted worker result.
	id, idErr := model.NewID("EVENT-")
	if idErr != nil {
		return errors.Join(err, idErr)
	}
	if r.store != nil {
		project, projectErr := r.store.Project(ctx)
		if projectErr != nil {
			return errors.Join(err, projectErr)
		}
		canonicalTaskID := ""
		if task, taskErr := r.store.GetTask(ctx, taskID); taskErr == nil {
			canonicalTaskID = task.ID
		}
		alertErr := r.store.AppendEvent(ctx, nil, model.Event{
			ID: id, Type: "HONEYPOT_HIT", ProjectID: project.ID, TaskID: canonicalTaskID, Timestamp: time.Now().UTC(),
			Data: map[string]any{"run_id": alert.RunID, "task_id": alert.TaskID, "parent_run_id": alert.ParentRunID, "reason": err.Error(), "action": "task stopped; do not merge", "operator_alert": true, "marshal_alert": true},
		})
		if alertErr == nil {
			r.egressMu.Lock()
			sink := r.egressAlert
			r.egressMu.Unlock()
			if sink != nil {
				alertErr = sink(alert)
			}
		}
		return errors.Join(err, alertErr)
	}
	return err
}

func (r *Runtime) guardHoneypotHandIn(ctx context.Context, taskID, worktree string, handin marshal.HandIn) (string, error) {
	r.honeypotMu.Lock()
	trap := r.honeypots[worktree]
	r.honeypotMu.Unlock()
	if trap == nil {
		return "", errors.New("honeypot: required scan identity unavailable")
	}
	data, err := json.Marshal(handin)
	if err != nil {
		return trap.Home, err
	}
	return trap.Home, r.checkHoneypot(ctx, taskID, trap, data, nil)
}

// Every scan binds its runtime identity and disposition to the scanned commit.
func (r *Runtime) recordHoneypotScan(ctx context.Context, taskID string, trap *worker.Honeypot, scanErr error) error {
	if r.store == nil {
		return errors.New("honeypot: durable scan store unavailable")
	}
	head, err := gitMarshal(ctx, trap.Worktree(), "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	outcome := "clean"
	if errors.Is(scanErr, worker.ErrHoneypot) {
		outcome = "hit"
	} else if scanErr != nil {
		outcome = "incomplete"
	}
	id, err := model.NewID("EVENT-")
	if err != nil {
		return err
	}
	project, err := r.store.Project(ctx)
	if err != nil {
		return err
	}
	return r.store.AppendEvent(ctx, nil, model.Event{ID: id, Type: "HONEYPOT_SCAN", ProjectID: project.ID, Timestamp: time.Now().UTC(), Data: map[string]any{"task_id": taskID, "scan_id": trap.Home, "result_commit": head, "outcome": outcome}})
}

func (s *MarshalService) checkHoneypotAdmission(ctx context.Context, task marshal.Task, commit string) error {
	events, err := s.Store.ListEvents(ctx)
	if err != nil {
		return err
	}
	clean := false
	for _, event := range events {
		if event.Type != "HONEYPOT_SCAN" || event.Data["result_commit"] != commit {
			continue
		}
		if event.Data["outcome"] == "hit" {
			return errors.New("honeypot: artifact quarantined")
		}
		if event.Data["outcome"] == "clean" && task.HoneypotScanID != "" && event.Data["scan_id"] == task.HoneypotScanID {
			clean = true
		}
	}
	if (task.HoneypotRequired || task.Mode == marshal.Governed && s.HandInGuard != nil) && !clean {
		return errors.New("honeypot: required clean scan missing")
	}
	return nil
}
