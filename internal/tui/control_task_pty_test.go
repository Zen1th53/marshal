//go:build linux

package tui

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

// These are candidate integration tests: I must install the factory and change
// the five manifest entries before they can qualify the compiled binary.
// PTMX/socket skips are unmet qualification and must be rerun unsandboxed.
func TestPTYTaskMenuPauseResumeRetryAndCancel(t *testing.T) {
	if !editionExtreme {
		t.Skip("candidate menu qualification requires (default build)")
	}
	openPTY(t)
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	project := initProject(t, bin)
	ctx := context.Background()
	const session = "SESSION-task-menu"
	r, err := app.Open(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e := app.CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: session, TargetID: "TASK-menu-selected", IdempotencyKey: "menu-fixture"}
	task, err := r.CommandTask(local.Context(ctx), e, app.TaskCommand{Operation: "create", Title: "selected menu task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.ImportTasks(ctx, []model.Task{{ID: "TASK-menu-failed", Title: "failed attempt retained", Status: model.TaskBlocked, Risk: model.R0}}); err != nil {
		t.Fatal(err)
	}
	fixture := newLocalCloudFixture(t)
	s := startTUIInProjectEnv(t, 40, 160, bin, project, map[string]string{"MARSHAL_CLOUD_ENDPOINT": fixture.URL()}, "tui", session)
	s.send("\x0e")
	taskMenuPTYAction(t, s, "Pause task")
	s.mustSee("Exact task ID")
	s.send(task.ID + "\r")
	s.mustSee("Proceed")
	s.send("\r") // Default Cancel must leave the durable row unchanged.
	unchanged, err := r.Task(ctx, task.ID)
	if err != nil || unchanged.Revision != task.Revision || unchanged.ControlState != "" {
		t.Fatalf("Cancel changed task: %+v %v", unchanged, err)
	}
	s.send("\x1b")
	taskMenuPTYAction(t, s, "Pause task")
	s.mustSee("Exact task ID")
	s.send(task.ID + "\r")
	s.mustSee("Proceed")
	s.send("\x1b[C\r")
	s.mustSee("pause committed at revision 2")
	paused, err := r.Task(ctx, task.ID)
	if err != nil || paused.ControlState != "paused" || paused.Revision != 2 {
		t.Fatalf("pause readback: %+v %v", paused, err)
	}
	s.send("\x1b")
	taskMenuPTYAction(t, s, "Resume task")
	s.mustSee("Exact task ID")
	s.send(task.ID + "\r")
	s.mustSee("Proceed")
	s.send("\x1b[C\r")
	s.mustSee("resume committed at revision 3")
	resumed, err := r.Task(ctx, task.ID)
	if err != nil || resumed.ControlState != "" || resumed.Status != model.TaskReady || resumed.Revision != 3 {
		t.Fatalf("resume readback: %+v %v", resumed, err)
	}
	s.send("\x1b")
	taskMenuPTYAction(t, s, "Retry task")
	s.mustSee("Exact task ID")
	s.send("TASK-menu-failed\r")
	s.mustSee("Proceed")
	s.send("\x1b[C\r")
	s.mustSee("retry committed")
	retried, err := r.Task(ctx, "TASK-menu-failed")
	if err != nil || retried.Status != model.TaskReady || retried.Attempt != 2 {
		t.Fatalf("retry readback: %+v %v", retried, err)
	}
}

func taskMenuPTYAction(t *testing.T, s *ptySession, title string) {
	t.Helper()
	s.send("\x0b")
	s.send(title)
	s.send("\r")
	s.send("\r")
}
