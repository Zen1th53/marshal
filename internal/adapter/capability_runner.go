package adapter

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/capability"
)

// CapabilityRunner enforces the broker contract immediately before process
// execution. Provider adapters remain unaware of authorization semantics.
type CapabilityRunner struct {
	base    ProcessRunner
	broker  capability.Broker
	subject capability.SubjectID
	task    capability.TaskID
}

func NewCapabilityRunner(base ProcessRunner, broker capability.Broker, subject, task string) ProcessRunner {
	return &CapabilityRunner{base: base, broker: broker, subject: capability.SubjectID(subject), task: capability.TaskID(task)}
}

func (r *CapabilityRunner) Run(ctx context.Context, command Command) (ProcessResult, error) {
	if r == nil || r.base == nil || r.broker == nil {
		return ProcessResult{}, capability.ErrDenied
	}
	decision, err := r.broker.Authorize(ctx, capability.Query{Subject: r.subject, TaskID: r.task, Kind: capability.KindShellExec, Resource: command.Path, Action: "exec"})
	if err != nil {
		return ProcessResult{}, err
	}
	if decision.Outcome != capability.OutcomeAllow {
		return ProcessResult{}, fmt.Errorf("%w: %s", capability.ErrDenied, decision.Reason)
	}
	return r.base.Run(ctx, command)
}
