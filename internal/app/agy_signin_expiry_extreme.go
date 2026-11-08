//go:build !governed

package app

import (
	"context"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
)

// agySigninExpiryNotice is the one owner notice of an agy sign-in expiry episode.
const agySigninExpiryNotice = "agy sign-in expired: start agy once to sign in again, then resume"

// agySigninExpiryEvent is the durable owner alert for that notice.
const agySigninExpiryEvent = "AGY_SIGNIN_EXPIRED"

// agySigninEpisode is the end of the current expiry episode. Like
// providerHealth it is process-wide: the sign-in belongs to the account.
var agySigninEpisode struct {
	sync.Mutex
	until time.Time
}

// noteAgySigninExpiry classifies a failed agy run from its own output. On a
// sign-in expiry it cools agy down and, once per episode, notifies the owner.
func (r *Runtime) noteAgySigninExpiry(ctx context.Context, task execution.TaskExecution, runErr error, result adapter.Result) (bool, error) {
	if !classifyAgySigninExpiry(runErr, result.ExitCode, result.Stdout, result.Stderr, result.EndedAt.Sub(result.StartedAt)) {
		return false, nil
	}
	now := time.Now()
	agySigninEpisode.Lock()
	ongoing := isProviderUnavailable("agy", now) && now.Before(agySigninEpisode.until)
	if !ongoing {
		agySigninEpisode.until = now.Add(agySigninExpiryCooldown)
		markProviderUnavailable("agy", agySigninEpisode.until)
	}
	agySigninEpisode.Unlock()
	if ongoing {
		return true, nil
	}
	return true, r.notifyAgySigninExpiry(context.WithoutCancel(ctx), task.RunID, task.CanonicalTaskID)
}

// notifyAgySigninExpiry persists the owner alert, then delivers it through the
// operator sink that reaches the Marshal inbox and activity line.
func (r *Runtime) notifyAgySigninExpiry(ctx context.Context, runID, taskID string) error {
	if r == nil || r.store == nil {
		return model.ErrUnavailable
	}
	id, err := model.NewID("EVENT-")
	if err != nil {
		return err
	}
	project, err := r.store.Project(ctx)
	if err != nil {
		return err
	}
	canonicalTaskID := ""
	if task, taskErr := r.store.GetTask(ctx, taskID); taskErr == nil {
		canonicalTaskID = task.ID
	}
	if err := r.store.AppendEvent(ctx, nil, model.Event{
		ID: id, Type: agySigninExpiryEvent, ProjectID: project.ID, TaskID: canonicalTaskID, Timestamp: time.Now().UTC(),
		Data: map[string]any{"run_id": runID, "task_id": taskID, "provider": "agy", "message": agySigninExpiryNotice, "operator_alert": true, "marshal_alert": true},
	}); err != nil {
		return err
	}
	r.egressMu.Lock()
	sink := r.egressAlert
	r.egressMu.Unlock()
	if sink == nil {
		return nil
	}
	return sink(EgressAlert{RunID: runID, TaskID: taskID, Worker: "agy", Message: agySigninExpiryNotice, Kind: agySigninExpiryReason, State: "failed"})
}
