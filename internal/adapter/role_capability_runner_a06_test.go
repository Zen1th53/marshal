package adapter

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
)

type roleRunnerBase struct{ calls int }

func (r *roleRunnerBase) Run(context.Context, Command) (ProcessResult, error) {
	r.calls++
	return ProcessResult{ExitCode: 0}, nil
}

func TestRoleCapabilityRunnerDeniesBeforeProviderProcess(t *testing.T) {
	base := &roleRunnerBase{}
	principal := authz.Principal{ID: "agent-1", Role: authz.Role{Name: "qa", Authorities: []authz.Authority{authz.AuthorityVerifyQA}}}
	broker := roleRunnerBroker{decision: capability.Decision{Outcome: capability.OutcomeAllow, MatchedGrant: "cap-1"}}
	runner := NewRoleCapabilityRunner(base, principal, "task-1", authz.AuthoritySourceWrite, broker)
	_, err := runner.Run(context.Background(), Command{Path: "/usr/bin/provider", Args: []string{"run"}})
	if err == nil || base.calls != 0 {
		t.Fatalf("err=%v provider calls=%d", err, base.calls)
	}
}

func TestRoleCapabilityRunnerAllowsOnlyComposedDecision(t *testing.T) {
	base := &roleRunnerBase{}
	principal := authz.Principal{ID: "agent-1", Role: authz.Role{Name: "developer", Authorities: []authz.Authority{authz.AuthoritySourceWrite}}}
	broker := roleRunnerBroker{decision: capability.Decision{Outcome: capability.OutcomeAllow, MatchedGrant: "cap-1"}}
	runner := NewRoleCapabilityRunner(base, principal, "task-1", authz.AuthoritySourceWrite, broker)
	if _, err := runner.Run(context.Background(), Command{Path: "/usr/bin/provider", Args: []string{"run"}}); err != nil || base.calls != 1 {
		t.Fatalf("err=%v provider calls=%d", err, base.calls)
	}
}

type roleRunnerBroker struct{ decision capability.Decision }

func (roleRunnerBroker) Grant(context.Context, capability.GrantRequest) (capability.Grant, error) {
	return capability.Grant{}, capability.ErrDenied
}
func (b roleRunnerBroker) Authorize(context.Context, capability.Query) (capability.Decision, error) {
	return b.decision, nil
}
func (roleRunnerBroker) Revoke(context.Context, capability.RevokeRequest) error {
	return capability.ErrDenied
}
