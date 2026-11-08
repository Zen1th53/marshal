package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/cell"
	"github.com/Zen1th53/marshal/internal/execution"
)

type cellMenuAuthority struct {
	fakeAuthority
	target   app.CellCommandTarget
	input    app.CellExecCommand
	destroy  app.CellDestroyCommand
	envelope app.CommandEnvelope
	proof    app.CellCommandProof
	calls    int
}

func (a *cellMenuAuthority) InspectCellCommand(context.Context, cell.CellID, string) (app.CellCommandTarget, error) {
	return a.target, nil
}
func (a *cellMenuAuthority) CommandExecCell(_ context.Context, e app.CommandEnvelope, input app.CellExecCommand) (app.CellCommandProof, error) {
	a.envelope = e
	a.input = input
	a.calls++
	return a.proof, nil
}
func (a *cellMenuAuthority) CommandDestroyCell(_ context.Context, e app.CommandEnvelope, input app.CellDestroyCommand) (app.CellCommandProof, error) {
	a.envelope = e
	a.destroy = input
	a.calls++
	return a.proof, nil
}

func TestCellMenuTypedActionAndDurableProof(t *testing.T) {
	for _, destroy := range []bool{false, true} {
		t.Run(map[bool]string{false: "exec", true: "destroy"}[destroy], func(t *testing.T) {
			target := app.CellCommandTarget{Cell: cell.Record{ID: "cell-reviewed", TaskID: "TASK-owned", State: cell.StateRunning, SpecDigest: "spec", Workspace: "/tmp/owned"}, TaskRevision: 4, RunID: "RUN-owned", RunVersion: 3, RunState: execution.RunRunning}
			a := &cellMenuAuthority{target: target}
			exit := 0
			a.proof = app.CellCommandProof{Cell: target.Cell, Outcome: "EXECUTED", ExitCode: &exit}
			id := ActionID("CTUI-0051")
			if destroy {
				id = "CTUI-0052"
				a.proof.Outcome = "DESTROYED"
				a.proof.Cell.State = cell.StateDestroyed
			}
			s := &ControlSource{Authority: a, ProjectID: "project", SessionID: "session"}
			b := executionActionBindings(s)[id]
			req := ActionRequest{Action: id, ProjectID: s.ProjectID, SessionID: s.SessionID, ApprovalID: "exact-approval", IdempotencyKey: "exact-key", Inputs: map[string]string{"cell_id": "cell-reviewed", "action": "workspace.status", "reason": "cleanup", "approval_id": "exact-approval"}}
			var err error
			req.Target, err = b.PrepareRequest(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			out, err := b.Execute(context.Background(), req)
			if err != nil || !out.Succeeded() {
				t.Fatalf("out=%+v %v", out, err)
			}
			if a.calls != 1 || a.envelope.TargetID != "cell-reviewed" || a.envelope.ExpectedVersion != 4 || a.envelope.IdempotencyKey != "exact-key" {
				t.Fatalf("envelope=%+v calls=%d", a.envelope, a.calls)
			}
			if destroy {
				if b.Safety != SafetyDestructive || a.destroy.Reason != "cleanup" || a.destroy.ApprovalID != "exact-approval" {
					t.Fatalf("destroy=%+v", a.destroy)
				}
			} else if a.input.Action != app.CellWorkspaceStatus || a.input.Target.Digest() != target.Digest() {
				t.Fatalf("input=%+v", a.input)
			}
			// Input substitution after preparation cannot borrow another approval.
			req.Inputs["approval_id"] = "different"
			if _, err = b.Execute(context.Background(), req); err == nil || a.calls != 1 {
				t.Fatal("approval substitution accepted")
			}
		})
	}
}

func TestCellMenuUnknownAndStaleAreNeverSuccess(t *testing.T) {
	a := &cellMenuAuthority{target: app.CellCommandTarget{Cell: cell.Record{ID: "cell-reviewed", State: cell.StateRunning}, TaskRevision: 1}, proof: app.CellCommandProof{Outcome: "UNKNOWN"}}
	s := &ControlSource{Authority: a, ProjectID: "project", SessionID: "session"}
	b := executionActionBindings(s)["CTUI-0051"]
	req := ActionRequest{ProjectID: s.ProjectID, SessionID: s.SessionID, Target: cellControlTarget(a.target), ApprovalID: "approval", Inputs: map[string]string{"approval_id": "approval", "action": "workspace.status"}}
	out, err := b.Execute(context.Background(), req)
	if err != nil || out.Succeeded() || out.Verdict != VerdictUnknown || !strings.Contains(out.Detail, "reconciliation") {
		t.Fatalf("out=%+v %v", out, err)
	}
	a.target.Cell.SpecDigest = "changed"
	if _, err = b.Execute(context.Background(), req); err == nil || a.calls != 1 {
		t.Fatal("stale spec executed")
	}
}

func TestCellMenuDestructionDefaultsToCancel(t *testing.T) {
	a := &cellMenuAuthority{target: app.CellCommandTarget{Cell: cell.Record{ID: "cell-reviewed", State: cell.StateRunning}, TaskRevision: 1}}
	s := &ControlSource{Authority: a, ProjectID: "project", SessionID: "session"}
	b := executionActionBindings(s)["CTUI-0052"]
	c := &Confirmation{}
	req := ActionRequest{Action: b.Action, ProjectID: s.ProjectID, SessionID: s.SessionID, ApprovalID: "approval", PreparedAt: time.Now(), Inputs: map[string]string{"cell_id": "cell-reviewed", "approval_id": "approval", "reason": "cleanup"}}
	if err := c.Begin(context.Background(), b, req); err != nil {
		t.Fatal(err)
	}
	if c.Selection() != 0 || c.Acknowledged() {
		t.Fatal("destruction did not default to unacknowledged Cancel")
	}
	if _, err := c.Submit(context.Background()); err == nil || a.calls != 0 {
		t.Fatalf("unacknowledged destruction executed: %v", err)
	}
}
