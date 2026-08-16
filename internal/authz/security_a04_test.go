package authz

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/capability"
)

type capabilityStub struct{ decision capability.Decision }

func (s capabilityStub) Grant(context.Context, capability.GrantRequest) (capability.Grant, error) {
	return capability.Grant{}, capability.ErrDenied
}
func (s capabilityStub) Authorize(context.Context, capability.Query) (capability.Decision, error) {
	return s.decision, nil
}
func (s capabilityStub) Revoke(context.Context, capability.RevokeRequest) error {
	return capability.ErrDenied
}

func TestCanWithCapabilityRequiresBothRoleAuthorityAndExactCapability(t *testing.T) {
	principal := Principal{ID: "agent-1", Role: Role{Name: "developer", Authorities: []Authority{AuthoritySourceWrite}}}
	query := capability.Query{Subject: "agent-1", TaskID: "task-1", Kind: capability.KindFilesystemWrite, Resource: "/repo/file", Action: "write"}
	denied, err := CanWithCapability(context.Background(), principal, AuthoritySourceWrite, "/repo/file", query, capabilityStub{decision: capability.Decision{Outcome: capability.OutcomeDeny, Reason: capability.CodeDenied}})
	if err == nil || denied.Allowed || denied.Reason != CodeDenied {
		t.Fatalf("denied=%#v err=%v", denied, err)
	}
	allowed, err := CanWithCapability(context.Background(), principal, AuthoritySourceWrite, "/repo/file", query, capabilityStub{decision: capability.Decision{Outcome: capability.OutcomeAllow, MatchedGrant: "cap-1"}})
	if err != nil || !allowed.Allowed || allowed.CapabilityGrantID != "cap-1" {
		t.Fatalf("allowed=%#v err=%v", allowed, err)
	}
}

func TestCanWithCapabilityRejectsProviderClaimAndForeignQuery(t *testing.T) {
	principal := Principal{ID: "agent-1", Role: Role{Name: "developer", Authorities: []Authority{AuthoritySourceWrite}}}
	query := capability.Query{Subject: "provider-admin", TaskID: "task-1", Kind: capability.KindFilesystemWrite, Resource: "/repo/file", Action: "write"}
	decision, err := CanWithCapability(context.Background(), principal, AuthoritySourceWrite, "/repo/file", query, capabilityStub{decision: capability.Decision{Outcome: capability.OutcomeAllow}})
	if err == nil || decision.Allowed || decision.Reason != CodeDenied {
		t.Fatalf("foreign decision=%#v err=%v", decision, err)
	}
}
