package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestTaskMemoryGrantRequiresPolicyAdminAndExistingIdentity(t *testing.T) {
	ctx := context.Background()
	rt, svc := openTestMemoryService(t)
	if _, err := rt.ImportTasks(ctx, []model.Task{{ID: "TASK-memory-grant", Title: "grant", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	agent := model.Agent{ID: "AGENT-grantee", ProjectID: "PROJECT-local", DisplayName: "grantee", Role: model.RoleDeveloper, Status: model.AgentRegistered}
	if err := rt.Store().RegisterAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	req := TaskMemoryGrantRequest{TaskID: "TASK-memory-grant", PrincipalID: agent.ID, PolicyDigest: "sha256:" + strings.Repeat("a", 64)}
	if _, err := svc.GrantTaskAccess(ctx, testPrincipal("AGENT-developer"), req); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("non-admin grant error = %v, want unauthorized", err)
	}
	admin := authz.Principal{ID: "AGENT-admin", Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	binding, err := svc.GrantTaskAccess(ctx, admin, req)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := svc.GrantTaskAccess(ctx, admin, req)
	if err != nil || retry.ID != binding.ID || !retry.BoundAt.Equal(binding.BoundAt) {
		t.Fatalf("idempotent grant = %#v, %v", retry, err)
	}
	if err := svc.RevokeTaskAccess(ctx, admin, binding.ID); err != nil {
		t.Fatal(err)
	}
	allowed, err := rt.Store().HasActiveRoleBinding(ctx, agent.ID, req.TaskID)
	if err != nil || allowed {
		t.Fatalf("active after revoke = %v, %v", allowed, err)
	}
	if _, err := svc.GrantTaskAccess(ctx, admin, req); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("revoked grant recreation error = %v, want conflict", err)
	}
}
