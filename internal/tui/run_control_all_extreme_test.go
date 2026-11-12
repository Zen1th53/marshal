//go:build !governed

package tui

import (
	"fmt"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// The shipped Marshal runs each task in its own lane under one parent run.
// "pause the run" pauses every running lane (the parent first, which pauses
// its lanes with it) and holds dispatch; "resume the run" resumes the parent
// and every lane. The lanes are opened by the production OpenTaskLane; no
// provider turn starts.
func TestPauseAllPausesEveryLane(t *testing.T) {
	w := startupCodexDecisionWorkspace(t)
	rt, ctx := w.runtime, t.Context()
	goal := model.GoalContract{ID: "GOAL-pause-all", SessionID: w.sessionID, ProjectID: rt.ProjectIdentity(), Revision: 1, OriginalRequest: "bounded fixture", DesiredOutcome: "bounded fixture", Confirmation: model.ConfirmationApproved, ConstitutionVersion: constitution.Current.String(), Risk: model.R1, AuthoritySource: "fixture"}
	if err := rt.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	e := rt.Execution().Engine()
	parent := execution.ExecutionRun{RunID: "parent-pause-all", ProjectID: projectid.ID(rt.ProjectIdentity()), SessionID: w.sessionID, GoalID: goal.ID, GoalRevision: 1, PlanID: "PLAN-pause", PlanVersion: 1, Mode: plan.ModeUltra, Delivery: execution.DeliveryPreserveBranch, BaseCommit: "fixture-base", State: execution.RunReady, Tasks: map[string]execution.TaskExecution{}, Provenance: execution.RunProvenance{RepoRoot: rt.ProjectRoot()}}
	for i := range 3 {
		id := fmt.Sprintf("task-%d", i)
		parent.Tasks[id] = execution.TaskExecution{RunID: parent.RunID, TaskID: id, CanonicalTaskID: fmt.Sprintf("TASK-pause-%d", i), State: execution.TaskPending, AssignedHarness: "codex", AssignedModel: "fixture", TargetFiles: []string{fmt.Sprintf("marker-%d.txt", i)}}
	}
	if err := e.RunStore().CreateRun(ctx, parent); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		lane, err := e.OpenTaskLane(ctx, parent.RunID, fmt.Sprintf("task-%d", i), parent.BaseCommit)
		if err != nil {
			t.Fatal(err)
		}
		lane.State = execution.RunRunning
		if err := e.RunStore().UpdateRun(ctx, *lane); err != nil {
			t.Fatal(err)
		}
	}
	installRunControlSeam(t, w)
	checkPauseResumeAll(t, w, ctx, 3)
}
