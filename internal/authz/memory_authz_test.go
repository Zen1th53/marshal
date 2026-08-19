package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT121MemoryAuthorizationSurface(t *testing.T) {
	evaluator := authz.NewMemoryAuthorizer()
	ctx := context.Background()

	readOnlyPrincipal := authz.Principal{
		ID: "agent-readonly",
		Role: authz.Role{
			Name:        "agent-reader",
			Authorities: []authz.Authority{authz.AuthorityTaskPlan},
		},
	}

	adminPrincipal := authz.Principal{
		ID: "operator-admin",
		Role: authz.Role{
			Name:        "operator",
			Authorities: []authz.Authority{authz.AuthorityPolicyAdmin},
		},
	}

	// 1. Read-only agent can recall in their assigned scope
	err := evaluator.Authorize(ctx, readOnlyPrincipal, authz.ActionMemoryRecall, "scope-task-1", model.MemoryDurable)
	if err != nil {
		t.Fatalf("expected read-only agent to recall in own scope, got: %v", err)
	}

	// 2. Read-only agent CANNOT promote to protected durable memory
	err = evaluator.Authorize(ctx, readOnlyPrincipal, authz.ActionMemoryPromote, "scope-task-1", model.MemoryDurable)
	if !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for read-only promotion, got: %v", err)
	}

	// 3. Admin principal CAN promote to protected memory
	err = evaluator.Authorize(ctx, adminPrincipal, authz.ActionMemoryPromote, "scope-task-1", model.MemoryDurable)
	if err != nil {
		t.Fatalf("expected admin to authorize promotion, got: %v", err)
	}

	// 4. Unknown action must FAIL CLOSED
	err = evaluator.Authorize(ctx, adminPrincipal, authz.MemoryAction("memory.hack_admin"), "scope-task-1", model.MemoryDurable)
	if !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("expected fail-closed on unknown memory action, got: %v", err)
	}
}
