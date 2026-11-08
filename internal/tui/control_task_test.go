package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

type taskMenuAuthority struct {
	fakeAuthority
	envelope app.CommandEnvelope
	command  app.TaskCommand
	settling bool
}

func (a *taskMenuAuthority) CommandTask(_ context.Context, e app.CommandEnvelope, c app.TaskCommand) (model.Task, error) {
	a.envelope, a.command = e, c
	a.task.Revision++
	switch c.Operation {
	case "pause":
		a.task.ControlState = "paused"
		if a.settling {
			a.task.ControlState = "pause-requested"
		}
	case "resume":
		a.task.ControlState = ""
	case "retry":
		a.task.ControlState = ""
		a.task.Attempt++
	}
	return a.task, nil
}

func TestTaskMenuControlsUseCommandTask(t *testing.T) {
	for id, op := range map[ActionID]string{"CTUI-0043": "pause", "CTUI-0044": "resume", "CTUI-0045": "retry"} {
		t.Run(op, func(t *testing.T) {
			a := &taskMenuAuthority{fakeAuthority: fakeAuthority{task: model.Task{ID: "TASK-selected", Revision: 7, Status: model.TaskReady}}}
			s := &ControlSource{Authority: a, ProjectID: "project", SessionID: "session"}
			b := executionActionBindings(s)[id]
			req := ActionRequest{Action: id, ProjectID: s.ProjectID, SessionID: s.SessionID, IdempotencyKey: "exact-key", Inputs: map[string]string{"task_id": "TASK-selected"}}
			target, err := b.PrepareRequest(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			req.Target = target
			out, err := b.Execute(context.Background(), req)
			if err != nil || !out.Succeeded() {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if a.command.Operation != op || a.envelope.TargetID != "TASK-selected" || a.envelope.ExpectedVersion != 7 || a.envelope.IdempotencyKey != "exact-key" || a.envelope.SessionID != "session" || a.envelope.ProjectID != "project" {
				t.Fatalf("wrong command: %+v %+v", a.envelope, a.command)
			}
		})
	}
}

func TestTaskMenuPauseReportsSettlement(t *testing.T) {
	a := &taskMenuAuthority{settling: true, fakeAuthority: fakeAuthority{task: model.Task{ID: "TASK-live", Revision: 2, Status: model.TaskWorking}}}
	s := &ControlSource{Authority: a, ProjectID: "project", SessionID: "session"}
	b := executionActionBindings(s)["CTUI-0043"]
	req := ActionRequest{Target: Target{Kind: "task", ID: a.task.ID, Revision: 2}, ProjectID: s.ProjectID, SessionID: s.SessionID, IdempotencyKey: "pause"}
	out, err := b.Execute(context.Background(), req)
	if err != nil || !out.Succeeded() || !strings.Contains(out.Detail, "settling") || strings.Contains(out.Detail, "task paused") {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestTaskMenuMissingAuthorityFailsClosed(t *testing.T) {
	s := &ControlSource{Authority: &fakeAuthority{}}
	b := executionActionBindings(s)["CTUI-0043"]
	if _, err := b.PrepareRequest(context.Background(), ActionRequest{Inputs: map[string]string{"task_id": "TASK-x"}}); err == nil {
		t.Fatal("missing authority accepted")
	}
}
