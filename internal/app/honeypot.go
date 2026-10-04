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

func (r *Runtime) checkHoneypot(ctx context.Context, taskID string, trap *worker.Honeypot, stdout, stderr []byte) error {
	err := trap.Check(ctx, stdout, stderr)
	if !errors.Is(err, worker.ErrHoneypot) {
		return err
	}
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
			Data: map[string]any{"task_id": taskID, "reason": err.Error(), "action": "task stopped; do not merge", "operator_alert": true, "marshal_alert": true},
		})
		if alertErr == nil {
			r.egressMu.Lock()
			sink := r.egressAlert
			r.egressMu.Unlock()
			if sink != nil {
				alertErr = sink(EgressAlert{TaskID: taskID, Kind: "honeypot", State: "failed", Message: taskID + ": honeypot hit; task stopped; do not merge"})
			}
		}
		return errors.Join(err, alertErr)
	}
	return err
}

func (r *Runtime) guardHoneypotHandIn(ctx context.Context, taskID, worktree string, handin marshal.HandIn) error {
	r.honeypotMu.Lock()
	trap := r.honeypots[worktree]
	r.honeypotMu.Unlock()
	if trap == nil {
		return nil
	}
	data, err := json.Marshal(handin)
	if err != nil {
		return err
	}
	return r.checkHoneypot(ctx, taskID, trap, data, nil)
}
