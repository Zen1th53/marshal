package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/capability"
)

func TestCapabilityGateDeniesBeforeProcessSideEffect(t *testing.T) {
	runner := &countingRunner{}
	gate := NewCapabilityGate(runner, denyingBroker{}, func(adapter.Command) capability.Query {
		return capability.Query{Subject: "agent-1", TaskID: "task-1", Kind: capability.KindShellExec, Resource: "/bin/echo", Action: "execute"}
	})
	_, err := gate.Run(context.Background(), adapter.Command{Path: "/bin/echo", Args: []string{"unsafe"}})
	if !errors.Is(err, capability.ErrDenied) || runner.calls != 0 {
		t.Fatalf("err=%v calls=%d, want denied and zero process calls", err, runner.calls)
	}
}

type countingRunner struct{ calls int }

func (r *countingRunner) Run(context.Context, adapter.Command) (adapter.ProcessResult, error) {
	r.calls++
	return adapter.ProcessResult{}, nil
}

type denyingBroker struct{}

func (denyingBroker) Grant(context.Context, capability.GrantRequest) (capability.Grant, error) {
	return capability.Grant{}, capability.ErrDenied
}
func (denyingBroker) Authorize(context.Context, capability.Query) (capability.Decision, error) {
	return capability.Decision{Reason: capability.ReasonDenied}, nil
}
func (denyingBroker) Revoke(context.Context, capability.RevokeRequest) error {
	return capability.ErrDenied
}
