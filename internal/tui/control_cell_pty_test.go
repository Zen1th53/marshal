//go:build linux

package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/cell"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// This exercises real menu selection/review/Cancel against durable fixtures.
// Positive external execution/destruction additionally requires I's governed
// backend composition and unsandboxed bubblewrap/process qualification.
func TestPTYCellMenuTypedReviewAndDestructiveCancel(t *testing.T) {
	if !editionExtreme {
		t.Skip("candidate menu qualification requires (default build)")
	}
	openPTY(t)
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	project := initProject(t, bin)
	ctx := context.Background()
	const session = "SESSION-cell-menu"
	r, err := app.Open(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := local.Context(ctx)
	goal := model.GoalContract{ID: "GOAL-cell-menu", SessionID: session, ProjectID: r.ProjectIdentity(), Revision: 1, OriginalRequest: "inspect workspace", DesiredOutcome: "inspect workspace", ConstitutionVersion: constitution.Current.String(), Confirmation: model.ConfirmationApproved, Risk: model.R1, AuthoritySource: "operator"}
	if err = r.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	e := app.CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: session, TargetID: "TASK-cell-menu", IdempotencyKey: "create-cell-fixture"}
	task, err := r.CommandTask(owner, e, app.TaskCommand{Operation: "create", Title: "cell menu fixture"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := r.RegisterAgent(ctx, app.RegisterAgentRequest{Name: "cell-menu-worker", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	e.ExpectedVersion = task.Revision
	e.IdempotencyKey = "assign-cell-fixture"
	task, err = r.CommandTask(owner, e, app.TaskCommand{Operation: "assign", AgentID: agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := r.Store().ActiveLease(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Store().BeginExecution(ctx, task.ID, lease.Lease.SessionID, agent.ID, "cell-menu", workspace, "fixture-base", task.Revision); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	// Persist test fixtures only; the PTY cancellation paths must not invoke
	// PrepareCell or perform external backend effects.
	record := cell.Record{ID: "cell-menu", TaskID: cell.TaskID(task.ID), Backend: cell.BackendBubblewrap, Workspace: workspace, SpecDigest: "fixture-spec", State: cell.StateRunning, CreatedAt: now, UpdatedAt: now}
	if err = r.Store().PutCell(ctx, record); err != nil {
		t.Fatal(err)
	}
	run := execution.ExecutionRun{RunID: "RUN-cell-menu", Version: 1, ProjectID: projectid.ID(r.ProjectIdentity()), SessionID: session, GoalID: goal.ID, GoalRevision: 1, State: execution.RunRunning, StartedAt: now, UpdatedAt: now, Tasks: map[string]execution.TaskExecution{"cell": {TaskID: "cell", CanonicalTaskID: task.ID, WorktreePath: workspace, State: execution.TaskRunning}}}
	if err = r.Execution().Engine().RunStore().CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	target, err := r.InspectCellCommand(ctx, record.ID, session)
	if err != nil {
		t.Fatal(err)
	}
	issueApproval := func(destroy bool) string {
		t.Helper()
		req, err := app.CellCommandApprovalBinding(target, app.CellWorkspaceStatus, destroy, "cleanup")
		if err != nil {
			t.Fatal(err)
		}
		req.RequestedBy = "worker"
		a, err := r.Execution().Engine().ApprovalManager().RequestApproval(req)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = r.Execution().Engine().ApprovalManager().Decide(a.ApprovalID, true, "operator", "reviewed fixture", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		return a.ApprovalID
	}
	execApproval, destroyApproval := issueApproval(false), issueApproval(true)
	fixture := newLocalCloudFixture(t)
	s := startTUIInProjectEnv(t, 40, 180, bin, project, map[string]string{"MARSHAL_CLOUD_ENDPOINT": fixture.URL()}, "tui", session)
	s.send("\x0e")
	taskMenuPTYAction(t, s, "Execute governed tool/action")
	s.mustSee("Exact cell ID")
	s.send("cell-menu\r")
	s.mustSee("workspace.status or workspace.diff")
	s.send("\r")
	s.mustSee("Exact canonical action approval ID")
	s.send(execApproval + "\r")
	s.mustSee("Proceed")
	s.send("\r")
	read, err := r.Store().GetCell(ctx, record.ID)
	if err != nil || cell.RecordDigest(read) != cell.RecordDigest(record) {
		t.Fatalf("execution Cancel changed cell: %+v %v", read, err)
	}
	s.send("\x1b")
	taskMenuPTYAction(t, s, "Destroy cell")
	s.mustSee("Exact cell ID")
	s.send("cell-menu\r")
	s.mustSee("Reason for destruction")
	s.send("cleanup\r")
	s.mustSee("Exact canonical destruction approval ID")
	s.send(destroyApproval + "\r")
	s.mustSee("Proceed")
	s.send("\r") // No 'y' acknowledgement; default Cancel cannot destroy.
	read, err = r.Store().GetCell(ctx, record.ID)
	if err != nil || read.State != cell.StateRunning || cell.RecordDigest(read) != cell.RecordDigest(record) {
		t.Fatalf("destructive Cancel changed cell: %+v %v", read, err)
	}
	approval, err := r.Execution().Engine().ApprovalManager().GetApproval(destroyApproval)
	if err != nil || approval.Status != execution.ApprovalApproved {
		t.Fatalf("Cancel consumed approval: %+v %v", approval, err)
	}
}
