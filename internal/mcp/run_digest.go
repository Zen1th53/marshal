package mcp

import (
	"context"
	"errors"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// runDigest is one small read of where the current Marshal run stands, so the
// Marshal reads state once per turn instead of polling events and tasks.
type runDigest struct {
	RunID           string           `json:"run_id,omitempty"`
	State           marshal.RunState `json:"state,omitempty"`
	Tier            marshal.Tier     `json:"tier,omitempty"`
	PlanID          string           `json:"plan_id,omitempty"`
	PlanVersion     int64            `json:"plan_version,omitempty"`
	Paused          string           `json:"paused,omitempty"`
	Resolutions     []string         `json:"resolutions,omitempty"`
	PauseCode       string           `json:"pause_code,omitempty"`
	PauseAction     string           `json:"pause_action,omitempty"`
	PauseTask       string           `json:"pause_task,omitempty"`
	CloseAuthorized bool             `json:"close_authorized"`
	// ReviewPolicy is the review policy bound to the run when it was approved.
	ReviewPolicy     *marshal.ReviewPolicySnapshot `json:"review_policy,omitempty"`
	Tasks            []runDigestTask               `json:"tasks"`
	PendingApprovals int                           `json:"pending_approvals,omitempty"`
	ExpiredApprovals int                           `json:"expired_approvals,omitempty"`
	Approvals        []runDigestApproval           `json:"approvals,omitempty"`
	EventsCursor     string                        `json:"events_cursor,omitempty"`
	NoRun            bool                          `json:"no_run,omitempty"`
}

type runDigestApproval struct {
	ApprovalID   string                   `json:"approval_id"`
	TaskID       string                   `json:"task"`
	Status       execution.ApprovalStatus `json:"status"`
	Authority    string                   `json:"authority"`
	Command      string                   `json:"command,omitempty"`
	ExpiresAt    *time.Time               `json:"expires_at,omitempty"`
	WhoCanAnswer string                   `json:"who_can_answer,omitempty"`
}

type runDigestTask struct {
	ID     string            `json:"id"`
	Title  string            `json:"title,omitempty"`
	Worker string            `json:"worker,omitempty"`
	State  marshal.TaskState `json:"state"`
	// Attempt is the task's current attempt.
	Attempt int `json:"attempt"`
}

func (s *Server) runDigest(ctx context.Context) (runDigest, error) {
	out := runDigest{Tasks: []runDigestTask{}}
	if events, err := s.runtime.Events(ctx); err == nil && len(events) > 0 {
		last := events[0]
		for _, e := range events[1:] {
			if afterCursor(e, last.Timestamp.UnixNano(), last.ID) {
				last = e
			}
		}
		out.EventsCursor = eventCursor(last)
	}
	bare := s.runtime.Marshal()
	if bare == nil || bare.ProjectID == "" {
		out.NoRun = true
		return out, nil
	}
	id, record, err := s.runtime.Store().LatestMarshalRun(ctx, bare.ProjectID)
	if errors.Is(err, model.ErrNotFound) {
		out.NoRun = true
		return out, nil
	}
	if err != nil {
		return out, err
	}
	run := record.Value
	out.RunID, out.State, out.Tier, out.PlanID, out.PlanVersion = id, run.State, run.Tier, run.PlanID, run.PlanVersion
	if run.Pause != nil {
		out.Paused, out.Resolutions = run.Pause.Reason, run.Pause.Resolutions
		out.PauseCode, out.PauseAction, out.PauseTask = run.Pause.Code, string(run.Pause.Action), run.Pause.TaskID
	}
	out.CloseAuthorized = run.ValidCloseAuthorization()
	out.ReviewPolicy = run.ReviewPolicy
	for _, t := range run.Tasks {
		out.Tasks = append(out.Tasks, runDigestTask{ID: t.PlanTaskID, Title: t.Title, Worker: t.Worker, State: t.State, Attempt: app.TaskAttempt(t)})
	}
	if views, _, projErr := s.runtime.ApprovalProjection(ctx, id, run, false); projErr == nil && len(views) > 0 {
		for _, v := range views {
			if v.Status == execution.ApprovalRequested {
				out.PendingApprovals++
			} else if v.Expired || v.Status == execution.ApprovalExpired {
				out.ExpiredApprovals++
			}
			out.Approvals = append(out.Approvals, runDigestApproval{
				ApprovalID:   v.ApprovalID,
				TaskID:       v.TaskID,
				Status:       v.Status,
				Authority:    v.Authority,
				Command:      v.Command,
				ExpiresAt:    v.ExpiresAt,
				WhoCanAnswer: v.WhoCanAnswer,
			})
		}
	}
	return out, nil
}
