//go:build !governed

package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// The shipped Marshal runs each task in its own lane under one parent run.
// "pause the run" pauses every running lane (the parent first, which pauses
// its lanes with it) and holds dispatch; "resume the run" resumes the parent
// and releases the hold, leaving each unfinished lane for the Marshal to
// continue under its own dispatch and collection (it reopens the same lane),
// so no lane restarts outside the Marshal. The lanes are opened by the
// production OpenTaskLane; no provider turn starts.
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
	lanes := map[string]bool{}
	for i := range 3 {
		lane, err := e.OpenTaskLane(ctx, parent.RunID, fmt.Sprintf("task-%d", i), parent.BaseCommit)
		if err != nil {
			t.Fatal(err)
		}
		lanes[lane.RunID] = true
		lane.State = execution.RunRunning
		if err := e.RunStore().UpdateRun(ctx, *lane); err != nil {
			t.Fatal(err)
		}
	}
	installRunControlSeam(t, w)
	checkPauseResumeAll(t, w, ctx, 3, lanes)
}

// pauseAllLaneWorkspace is TestPauseAllPausesEveryLane's parent with three
// running lanes opened by the production OpenTaskLane, and the run control
// seam installed. It returns the lane ids in task order.
func pauseAllLaneWorkspace(t *testing.T) (*Workspace, []string) {
	t.Helper()
	w := startupCodexDecisionWorkspace(t)
	rt, ctx := w.runtime, t.Context()
	goal := model.GoalContract{ID: "GOAL-pause-lane", SessionID: w.sessionID, ProjectID: rt.ProjectIdentity(), Revision: 1, OriginalRequest: "bounded fixture", DesiredOutcome: "bounded fixture", Confirmation: model.ConfirmationApproved, ConstitutionVersion: constitution.Current.String(), Risk: model.R1, AuthoritySource: "fixture"}
	if err := rt.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	e := rt.Execution().Engine()
	parent := execution.ExecutionRun{RunID: "parent-pause-lane", ProjectID: projectid.ID(rt.ProjectIdentity()), SessionID: w.sessionID, GoalID: goal.ID, GoalRevision: 1, PlanID: "PLAN-pause", PlanVersion: 1, Mode: plan.ModeUltra, Delivery: execution.DeliveryPreserveBranch, BaseCommit: "fixture-base", State: execution.RunReady, Tasks: map[string]execution.TaskExecution{}, Provenance: execution.RunProvenance{RepoRoot: rt.ProjectRoot()}}
	for i := range 3 {
		id := fmt.Sprintf("task-%d", i)
		parent.Tasks[id] = execution.TaskExecution{RunID: parent.RunID, TaskID: id, CanonicalTaskID: fmt.Sprintf("TASK-pause-%d", i), State: execution.TaskPending, AssignedHarness: "codex", AssignedModel: "fixture", TargetFiles: []string{fmt.Sprintf("marker-%d.txt", i)}}
	}
	if err := e.RunStore().CreateRun(ctx, parent); err != nil {
		t.Fatal(err)
	}
	var lanes []string
	for i := range 3 {
		lane, err := e.OpenTaskLane(ctx, parent.RunID, fmt.Sprintf("task-%d", i), parent.BaseCommit)
		if err != nil {
			t.Fatal(err)
		}
		lanes = append(lanes, lane.RunID)
		lane.State = execution.RunRunning
		if err := e.RunStore().UpdateRun(ctx, *lane); err != nil {
			t.Fatal(err)
		}
	}
	installRunControlSeam(t, w)
	return w, lanes
}

// A lane paused mid-turn is still owned by its executor, which finishes the
// turn: "resume the run" resumes it like any run, so that executor ends the
// lane as it ends one never paused (v0.2.12 left it paused for the Marshal,
// and the finished lane stayed paused until the run's final check stopped on
// it). Lanes paused before their turn are still left for the Marshal.
func TestResumeAllResumesALanePausedMidTurn(t *testing.T) {
	w, lanes := pauseAllLaneWorkspace(t)
	old := runExecuting
	t.Cleanup(func() { runExecuting = old })
	runExecuting = func(_ *app.ExecutionService, runID string) bool { return runID == lanes[0] }
	checkPauseResumeAll(t, w, t.Context(), 3, map[string]bool{lanes[1]: true, lanes[2]: true})
}

// Resuming one lane paused before its turn (/resume run:<id>) leaves it for
// the Marshal like "resume the run" does, instead of marking it running
// with no executor, which the Marshal's next dispatch refuses as an orphan.
// Once nothing else waits for the person, the hold is released.
func TestResumeOneParkedLaneLeavesItToTheMarshal(t *testing.T) {
	w, lanes := pauseAllLaneWorkspace(t)
	ctx := t.Context()
	if out, err := w.ExecuteCommand(ctx, "/pause all"); err != nil || out != "Paused 3 running tasks." {
		t.Fatalf("/pause all: %q %v", out, err)
	}
	parent := "parent-pause-lane"
	if out, err := w.ExecuteCommand(ctx, "/resume run:"+parent); err != nil || !strings.Contains(out, "resumed") {
		t.Fatalf("/resume run:%s: %q %v", parent, out, err)
	}
	m := w.marshalSession()
	for i, lane := range lanes {
		out, err := w.ExecuteCommand(ctx, "/resume run:"+lane)
		if err != nil || out != "Run "+lane+" resumed; the Marshal continues its task." {
			t.Fatalf("/resume run:%s: %q %v", lane, out, err)
		}
		if state := runStates(t, w, ctx)[lane]; state != execution.RunPaused {
			t.Fatalf("%s is %s: resumed outside the Marshal", lane, state)
		}
		if held := m.isHeldFor("RUN-marshal", "T-01"); held {
			t.Fatalf("after resuming lane %d the Marshal is still held", i)
		}
	}
	if w.runResumable.Load() != 0 {
		t.Fatalf("the palette still offers resume for %d runs", w.runResumable.Load())
	}
}
