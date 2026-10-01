//go:build linux

package tui

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"testing"
)

func TestPTYTaskCreateAssignCancelReadback(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	project := initProject(t, bin)
	r, err := app.Open(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	agent, err := r.RegisterAgent(context.Background(), app.RegisterAgentRequest{Name: "pty-task-agent", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	terminal := startFrozenTUIInProject(t, 40, 180, bin, project, "tui", "SESSION-task-pty")
	terminal.sendLine("/task create PTY lifecycle proof")
	terminal.mustSee("attempt 1): PTY lifecycle proof")
	tasks, err := r.Tasks(context.Background())
	if err != nil || len(tasks) != 1 || tasks[0].Status != model.TaskReady || tasks[0].Revision != 1 {
		t.Fatalf("create read-back: %+v %v", tasks, err)
	}
	id := tasks[0].ID
	terminal.sendLine(fmt.Sprintf("/task assign %s %s", id, agent.ID))
	terminal.mustSee("claimed owner " + agent.ID)
	assigned, err := r.Task(context.Background(), id)
	if err != nil || assigned.OwnerAgentID == nil || *assigned.OwnerAgentID != agent.ID || assigned.Status != model.TaskClaimed || assigned.Revision != 2 {
		t.Fatalf("assign read-back: %+v %v", assigned, err)
	}
	lease, err := r.Store().ActiveLease(context.Background(), id)
	if err != nil || lease.AgentID != agent.ID {
		t.Fatalf("lease read-back: %+v %v", lease, err)
	}
	terminal.sendLine("/task cancel " + id)
	terminal.mustSee("cancelled (revision 3")
	cancelled, err := r.Task(context.Background(), id)
	if err != nil || cancelled.Status != model.TaskCancelled || cancelled.OwnerAgentID != nil {
		t.Fatalf("cancel read-back: %+v %v", cancelled, err)
	}
	terminal.sendLine("/tasks")
	terminal.mustSee("cancelled")
	terminal.sendLine("/quit")
	if err := terminal.cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}
