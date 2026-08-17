package adapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
)

type capabilityRunnerBroker struct{ decision capability.Decision }

func (b capabilityRunnerBroker) Grant(context.Context, capability.GrantRequest) (capability.Grant, error) {
	return capability.Grant{}, nil
}
func (b capabilityRunnerBroker) Authorize(context.Context, capability.Query) (capability.Decision, error) {
	return b.decision, nil
}
func (b capabilityRunnerBroker) Revoke(context.Context, capability.RevokeRequest) error { return nil }

type countingRunner struct{ calls int }

func (r *countingRunner) Run(context.Context, Command) (ProcessResult, error) {
	r.calls++
	return ProcessResult{ExitCode: 0, Isolation: model.IsolationCapability{Level: model.IsolationProcessOnly}, StartedAt: time.Now(), EndedAt: time.Now()}, nil
}

func TestCapabilityRunnerDenialPreventsProcessExecution(t *testing.T) {
	base := &countingRunner{}
	runner := NewCapabilityRunner(base, capabilityRunnerBroker{decision: capability.Decision{Outcome: capability.OutcomeDeny, Reason: capability.CodeDenied}}, "agent-1", "task-1")
	_, err := runner.Run(context.Background(), Command{Path: "/bin/sh", Args: []string{"-c", "true"}, Dir: "/workspace"})
	if !errors.Is(err, capability.ErrDenied) || base.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, base.calls)
	}
}
