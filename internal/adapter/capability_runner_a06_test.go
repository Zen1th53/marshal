package adapter

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
)

type capabilityRunnerBroker struct {
	decision capability.Decision
	err      error
}

func (b capabilityRunnerBroker) Grant(context.Context, capability.GrantRequest) (capability.Grant, error) {
	return capability.Grant{}, errors.New("not used")
}
func (b capabilityRunnerBroker) Authorize(context.Context, capability.Query) (capability.Decision, error) {
	return b.decision, b.err
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

func TestCapabilityRunnerPreservesBrokerAuthorizationError(t *testing.T) {
	cause := errors.New("broker transport failure")
	cases := []struct {
		name      string
		brokerErr error
		want      error
	}{
		{"expired", fmt.Errorf("%w: %w", capability.ErrExpired, cause), capability.ErrExpired},
		{"revoked", fmt.Errorf("%w: %w", capability.ErrRevoked, cause), capability.ErrRevoked},
		{"denied", fmt.Errorf("%w: %w", capability.ErrDenied, cause), capability.ErrDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := &capabilityCountingRunner{}
			runner := NewCapabilityRunner(base, capabilityRunnerBroker{err: tc.brokerErr}, "agent-1", "task-1")
			_, err := runner.Run(context.Background(), Command{Path: "/usr/bin/provider", Args: []string{"exec"}})
			if err == nil {
				t.Fatal("an authorization failure must not be reported as success")
			}
			if !errors.Is(err, tc.brokerErr) {
				t.Fatalf("authorization error identity was not preserved: got %v, want chain containing %v", err, tc.brokerErr)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want errors.Is %v", err, tc.want)
			}
			if !errors.Is(err, cause) {
				t.Fatalf("underlying cause was lost: got %v, want chain containing %v", err, cause)
			}
			var capabilityErr *capability.Error
			if !errors.As(err, &capabilityErr) {
				t.Fatalf("error=%v, want a *capability.Error in the chain", err)
			}
			if tc.want != capability.ErrDenied && errors.Is(err, capability.ErrDenied) {
				t.Fatalf("error=%v must retain %v instead of collapsing to ErrDenied", err, tc.want)
			}
			if base.calls != 0 {
				t.Fatalf("base runner calls=%d, want 0 after authorization failure", base.calls)
			}
		})
	}
}

func TestCapabilityRunnerRejectsMissingScope(t *testing.T) {
	for _, tc := range []struct{ subject, task, path string }{
		{"", "task-1", "/usr/bin/provider"},
		{"agent-1", "", "/usr/bin/provider"},
		{"agent-1", "task-1", " "},
	} {
		base := &capabilityCountingRunner{}
		runner := NewCapabilityRunner(base, capabilityRunnerBroker{decision: capability.Decision{Outcome: capability.OutcomeAllow}}, tc.subject, tc.task)
		_, err := runner.Run(context.Background(), Command{Path: tc.path})
		if !errors.Is(err, capability.ErrDenied) || base.calls != 0 {
			t.Fatalf("scope=%+v error=%v calls=%d", tc, err, base.calls)
		}
	}
}

func TestCapabilityRunnerDecisionReasonsRemainDenied(t *testing.T) {
	for _, tc := range []struct {
		reason capability.ErrorCode
		cause  error
	}{
		{capability.CodeExpired, capability.ErrExpired},
		{capability.CodeRevoked, capability.ErrRevoked},
		{capability.CodeInvalidScope, capability.ErrInvalidScope},
	} {
		base := &capabilityCountingRunner{}
		runner := NewCapabilityRunner(base, capabilityRunnerBroker{decision: capability.Decision{Outcome: capability.OutcomeDeny, Reason: tc.reason}}, "agent-1", "task-1")
		_, err := runner.Run(context.Background(), Command{Path: "/usr/bin/provider"})
		if !errors.Is(err, capability.ErrDenied) || !errors.Is(err, tc.cause) || base.calls != 0 {
			t.Fatalf("reason=%s error=%v calls=%d", tc.reason, err, base.calls)
		}
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
