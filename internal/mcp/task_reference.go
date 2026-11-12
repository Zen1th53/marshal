package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// runTaskView is task_get's answer for a task of the last run that has no
// store record of its own: what the Board and run_digest show for it.
type runTaskView struct {
	ID           string            `json:"id"`
	Title        string            `json:"title,omitempty"`
	State        marshal.TaskState `json:"state"`
	Worker       string            `json:"worker,omitempty"`
	Files        []string          `json:"files,omitempty"`
	DependsOn    []string          `json:"depends_on,omitempty"`
	ResultCommit string            `json:"result_commit,omitempty"`
	RunID        string            `json:"run_id"`
}

// taskByReference answers task_get. The full store ID reads that task. A task
// the Board, run_digest and the run-closed notice name by its short plan name
// ("greet") resolves within the current or last run: the exact plan name
// first, else one plan name or title equal ignoring case. More than one match
// is refused with the candidates; nothing matching keeps the not-found error.
func (s *Server) taskByReference(ctx context.Context, ref string) (any, error) {
	task, err := s.runtime.Task(ctx, ref)
	if err == nil || !errors.Is(err, model.ErrNotFound) {
		return task, err
	}
	project, perr := s.runtime.Store().Project(ctx)
	if perr != nil || project.ID == "" {
		return nil, err
	}
	runID, record, rerr := s.runtime.Store().LatestMarshalRun(ctx, project.ID)
	if rerr != nil {
		return nil, err
	}
	run := record.Value
	var matches []marshal.Task
	for _, t := range run.Tasks {
		if t.PlanTaskID == ref {
			matches = []marshal.Task{t}
			break
		}
		if strings.EqualFold(t.PlanTaskID, ref) || strings.EqualFold(strings.TrimSpace(t.Title), ref) {
			matches = append(matches, t)
		}
	}
	switch len(matches) {
	case 0:
		return nil, err
	case 1:
	default:
		names := make([]string, 0, len(matches))
		for _, t := range matches {
			names = append(names, fmt.Sprintf("%s (%s)", t.PlanTaskID, t.Title))
		}
		return nil, fmt.Errorf("%w: %q names more than one task of the last run: %s; pass one of these names", model.ErrInvalid, ref, strings.Join(names, ", "))
	}
	t := matches[0]
	if stored, serr := s.runtime.Task(ctx, app.PlanTaskStoreID(run.PlanID, run.PlanVersion, t.PlanTaskID)); serr == nil {
		return stored, nil
	} else if !errors.Is(serr, model.ErrNotFound) {
		return nil, serr
	}
	return runTaskView{ID: t.PlanTaskID, Title: t.Title, State: t.State, Worker: t.Worker, Files: t.Files, DependsOn: t.DependsOn, ResultCommit: t.ResultCommit, RunID: runID}, nil
}
