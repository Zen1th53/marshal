package mcp

import (
	"context"
	"errors"

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
	CloseAuthorized bool             `json:"close_authorized"`
	Tasks           []runDigestTask  `json:"tasks"`
	EventsCursor    string           `json:"events_cursor,omitempty"`
	NoRun           bool             `json:"no_run,omitempty"`
}

type runDigestTask struct {
	ID     string            `json:"id"`
	Title  string            `json:"title,omitempty"`
	Worker string            `json:"worker,omitempty"`
	State  marshal.TaskState `json:"state"`
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
	}
	out.CloseAuthorized = run.ValidCloseAuthorization()
	for _, t := range run.Tasks {
		out.Tasks = append(out.Tasks, runDigestTask{ID: t.PlanTaskID, Title: t.Title, Worker: t.Worker, State: t.State})
	}
	return out, nil
}
