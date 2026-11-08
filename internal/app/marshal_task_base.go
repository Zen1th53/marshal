package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// resolveDispatchTaskBase keeps briefing freshness and dispatch's execution
// base on the same read-only calculation. It does not update the approved run.
func (s *MarshalService) resolveDispatchTaskBase(ctx context.Context, runID string, run marshal.Run, task marshal.Task) (marshal.Task, error) {
	fresh := task.State == marshal.Reassigned && task.ResultCommit != ""
	if (task.ResultCommit == "" || fresh) && len(task.DependsOn) > 0 {
		integration := filepath.Join(s.Worktrees, integrationTaskID(runID, run))
		head, err := gitMarshal(ctx, integration, "rev-parse", "HEAD")
		if err != nil {
			return task, fmt.Errorf("task base is unavailable: %w", err)
		}
		task.BaseCommit = strings.TrimSpace(head)
	}
	i := taskIndex(run, task.PlanTaskID)
	if serialProcess05Base(s.Gate, run) && task.Mode == marshal.Governed && task.ResultCommit == "" && i > 0 {
		integration := filepath.Join(s.Worktrees, integrationTaskID(runID, run))
		if head, err := gitMarshal(ctx, integration, "rev-parse", "HEAD"); err == nil {
			task.BaseCommit = head
		} else if run.Tasks[i-1].State == marshal.Merged {
			return task, fmt.Errorf("governed task base is unavailable: %w", err)
		}
	}
	return task, nil
}
