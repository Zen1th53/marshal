package authz

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
)

func TestAuthorityBoundaryRejectsProviderAdminAndSecretBearingIdentity(t *testing.T) {
	marker := "MARSHAL_TEST_SECRET_T04_A07_2f8c"
	principal := Principal{ID: marker, Role: Role{Name: "developer", Authorities: []Authority{AuthoritySourceWrite}}}
	decision, err := Can(context.Background(), principal, AuthorityPolicyAdmin, "worktree:/repo")
	if err == nil || decision.Allowed || !strings.Contains(err.Error(), "denied") || strings.Contains(err.Error(), marker) {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
}

func TestRoleDoesNotBypassConcreteCapability(t *testing.T) {
	principal := Principal{ID: "agent-1", Role: Role{Name: "developer", Authorities: []Authority{AuthoritySourceWrite}}}
	decision, err := CanWithCapability(context.Background(), principal, AuthoritySourceWrite, "/repo/file", capability.Query{Subject: "agent-1", TaskID: "task-1", Kind: capability.KindFilesystemWrite, Resource: "/repo/file", Action: "write"}, capabilityStub{decision: capability.Decision{Outcome: capability.OutcomeDeny, Reason: capability.CodeDenied}})
	if err == nil || decision.Allowed {
		t.Fatalf("role bypassed capability: decision=%#v err=%v", decision, err)
	}
}

func FuzzRoleBindingNameNeverPanics(f *testing.F) {
	for _, seed := range []string{"developer", "release-reviewer", "../admin", "", "MARSHAL_TEST_SECRET_T04_A07"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		binding := RoleBinding{ID: "binding", PrincipalID: "agent", Role: name, ScopeID: "task:1", BoundBy: "admin", BoundAt: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC), PolicyDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
		_ = binding.Validate()
	})
}
