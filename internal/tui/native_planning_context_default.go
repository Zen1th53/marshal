//go:build governed

package tui

import (
	"context"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func (*Workspace) persistNativePlanningDraft(ctx context.Context, _ string, service *app.MarshalService, runID string, draft app.MarshalDraft, budget marshal.Budget) (marshal.Run, error) {
	goal := draft.Plan.Goal.GoalID
	if goal == "" {
		goal = draft.Plan.ID
	}
	return service.StartPlanningFromDraft(ctx, runID, goal, draft, budget)
}

func (*Workspace) prepareUnleasedNativePlanningLaunch(_ context.Context, _, _ string, args []string, _ *briefingDir) ([]string, error) {
	return args, nil
}
