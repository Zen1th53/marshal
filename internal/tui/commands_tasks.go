package tui

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/google/uuid"
	"strings"
)

func (h *CommandHandler) handleTaskMutation(ctx context.Context, args []string) (string, error) {
	input := app.TaskCommand{Operation: strings.ToLower(args[0])}
	switch {
	case input.Operation == "create" && strings.TrimSpace(strings.Join(args[1:], " ")) == "":
		return "Usage: /task create <title>", nil
	case input.Operation == "assign" && len(args) != 3:
		return "Usage: /task assign <id> <agent>", nil
	case input.Operation != "create" && input.Operation != "assign" && len(args) != 2:
		return fmt.Sprintf("Usage: /task %s <id>", input.Operation), nil
	}
	a, ok := h.ws.controlSource().Authority.(*runtimeControlAuthority)
	if !ok || a == nil || a.runtime == nil || a.localControl == nil {
		return "Task mutation is unavailable in TUI: authenticated runtime authorization is required.", nil
	}
	e := app.CommandEnvelope{ProjectID: a.runtime.ProjectIdentity(), SessionID: a.sessionID, IdempotencyKey: uuid.NewString()}
	if input.Operation == "create" {
		id, err := model.NewID("TASK-")
		if err != nil {
			return "", err
		}
		e.TargetID = id
		input.Title = strings.Join(args[1:], " ")
	} else {
		task, err := a.runtime.Task(ctx, args[1])
		if err != nil {
			return "", err
		}
		e.TargetID = task.ID
		e.ExpectedVersion = task.Revision
		if input.Operation == "assign" {
			input.AgentID = args[2]
		}
	}
	task, err := a.runtime.CommandTask(a.localControl.Context(ctx), e, input)
	if err != nil {
		return "", err
	}
	status := string(task.Status)
	if task.ControlState != "" {
		status = task.ControlState
	}
	owner := ""
	if task.OwnerAgentID != nil {
		owner = " owner " + *task.OwnerAgentID
	}
	return fmt.Sprintf("Task %s %s%s (revision %d, attempt %d): %s", task.ID, status, owner, task.Revision, task.Attempt, task.Title), nil
}
