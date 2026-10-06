package tui

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

func validateOpenCodeDispatchAgent(agent model.Agent) error {
	if agent.Role != model.RoleDeveloper || agent.Status == model.AgentDisabled || agent.ModelProvider != "opencode" {
		return fmt.Errorf("governed OpenCode dispatch requires an enabled developer bound to opencode")
	}
	return nil
}

// startOpenCodeDispatch keeps the composer available for live exact-host grants.
// Runtime.Run owns the sandbox, proxy, provider process and hand-in guards.
func (w *Workspace) startOpenCodeDispatch(ctx context.Context, args []string) (string, error) {
	if len(args) != 3 && len(args) != 4 {
		return "Usage: /opencode dispatch <task-id> <agent-id> [model]", nil
	}
	if w.runtime == nil {
		return "", fmt.Errorf("runtime unavailable")
	}
	task, err := w.runtime.Task(ctx, args[1])
	if err != nil {
		return "", err
	}
	agent, err := w.runtime.Store().GetAgent(ctx, args[2])
	if err != nil {
		return "", err
	}
	if err := validateOpenCodeDispatchAgent(agent); err != nil {
		return "", err
	}
	selected := agent.ModelName
	if len(args) == 4 {
		selected = args[3]
	}
	if selected == "" {
		return "", fmt.Errorf("an explicit model is required")
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	w.mu.Lock()
	if w.governedDispatches == nil {
		w.governedDispatches = map[string]context.CancelFunc{}
	}
	if w.governedDispatches[task.ID] != nil {
		w.mu.Unlock()
		cancel()
		return "", fmt.Errorf("task is already dispatching")
	}
	w.governedDispatches[task.ID] = cancel
	w.mu.Unlock()
	w.governedDispatchWG.Add(1)
	go func() {
		defer w.governedDispatchWG.Done()
		defer cancel()
		defer func() { w.mu.Lock(); delete(w.governedDispatches, task.ID); w.mu.Unlock() }()
		result, err := w.runtime.Run(runCtx, app.RunRequest{TaskID: task.ID, AgentID: agent.ID, Adapter: "opencode", Model: selected, ExpectedRevision: task.Revision, NetworkRequired: true})
		if err != nil {
			w.RecordActivity("OpenCode task " + task.ID + " failed: " + err.Error())
			return
		}
		w.RecordActivity(fmt.Sprintf("OpenCode task %s: %s · run %s · isolation %s", task.ID, result.Status, result.RunID, result.Isolation.Level))
	}()
	return "Started governed OpenCode task " + task.ID + "; /egress status shows live requests.", nil
}

func (w *Workspace) cancelGovernedDispatches() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, cancel := range w.governedDispatches {
		cancel()
	}
}
