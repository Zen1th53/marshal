package adapter

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/capability"
)

// CapabilityRunner is the provider-neutral process boundary. It asks the
// canonical broker before delegating to any OS/process runner.
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
	if r == nil || r.base == nil || r.broker == nil || strings.TrimSpace(string(r.subject)) == "" ||
		strings.TrimSpace(string(r.task)) == "" || strings.TrimSpace(command.Path) == "" {
		return ProcessResult{}, capability.ErrDenied
	}
	if err := ctx.Err(); err != nil {
		return ProcessResult{}, err
	}
	decision, err := r.broker.Authorize(ctx, capability.Query{
		Subject: r.subject, TaskID: r.task, Kind: capability.KindShellExec,
		Resource: command.Path, Action: "execute",
	})
	if err != nil {
		return ProcessResult{}, capability.ErrDenied
	}
	if decision.Outcome != capability.OutcomeAllow {
		return ProcessResult{}, decisionError(decision.Reason)
	}
	return r.base.Run(ctx, command)
}

func decisionError(reason capability.ErrorCode) error {
	switch reason {
	case capability.CodeExpired:
		return capability.ErrExpired
	case capability.CodeRevoked:
		return capability.ErrRevoked
	case capability.CodeSubjectMismatch:
		return capability.ErrSubjectMismatch
	case capability.CodeTaskMismatch:
		return capability.ErrTaskMismatch
	case capability.CodeInvalidScope:
		return capability.ErrInvalidScope
	case capability.CodeDenied:
		return capability.ErrDenied
	default:
		return fmt.Errorf("%w: unknown capability decision", capability.ErrDenied)
	}
}
