package adapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
)

type capabilityRunnerBroker struct {
	decision capability.Decision
}

func (b capabilityRunnerBroker) Grant(context.Context, capability.GrantRequest) (capability.Grant, error) {
	return capability.Grant{}, errors.New("not used")
}
func (b capabilityRunnerBroker) Authorize(context.Context, capability.Query) (capability.Decision, error) {
	return b.decision, nil
}
func (b capabilityRunnerBroker) Revoke(context.Context, capability.RevokeRequest) error {
	return errors.New("not used")
}

type capabilityCountingRunner struct{ calls int }

func (r *capabilityCountingRunner) Run(_ context.Context, _ Command) (ProcessResult, error) {
	r.calls++
	return ProcessResult{ExitCode: 0, StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC()}, nil
}

func TestCapabilityRunnerDeniesBeforeProcessSideEffect(t *testing.T) {
	base := &capabilityCountingRunner{}
	runner := NewCapabilityRunner(base, capabilityRunnerBroker{decision: capability.Decision{Outcome: capability.OutcomeDeny, Reason: capability.CodeDenied}}, "agent-1", "task-1")
	_, err := runner.Run(context.Background(), Command{Path: "/usr/bin/provider", Args: []string{"exec"}})
	if !errors.Is(err, capability.ErrDenied) {
		t.Fatalf("error=%v, want ErrDenied", err)
	}
	if base.calls != 0 {
		t.Fatalf("base runner calls=%d, want 0", base.calls)
	}
}

func TestCapabilityRunnerAllowsCanonicalProcessBoundary(t *testing.T) {
	base := &capabilityCountingRunner{}
	runner := NewCapabilityRunner(base, capabilityRunnerBroker{decision: capability.Decision{Outcome: capability.OutcomeAllow}}, "agent-1", "task-1")
	result, err := runner.Run(context.Background(), Command{Path: "/usr/bin/provider", Args: []string{"exec"}})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if base.calls != 1 {
		t.Fatalf("base runner calls=%d, want 1", base.calls)
	}
}
