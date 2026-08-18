package authz

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBindingTransitionRequiresExplicitStateAndAuthenticatedBinder(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	binding := RoleBinding{ID: "binding-a03", PrincipalID: "agent-1", Role: "developer", ScopeID: "task:1", BoundBy: "admin", BoundAt: now, PolicyDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	bound, err := TransitionBinding(context.Background(), binding, BindingTransition{Actor: "admin", From: StateUnbound, To: StateBound})
	if err != nil || bound.State != StateBound {
		t.Fatalf("bound=%#v err=%v", bound, err)
	}
	changed, err := TransitionBinding(context.Background(), bound, BindingTransition{Actor: "admin", From: StateBound, To: StateChanged})
	if err != nil || changed.State != StateChanged {
		t.Fatalf("changed=%#v err=%v", changed, err)
	}
	if _, err := TransitionBinding(context.Background(), changed, BindingTransition{Actor: "foreign", From: StateChanged, To: StateRevoked}); !errors.Is(err, ErrDenied) {
		t.Fatalf("foreign transition err=%v", err)
	}
}

func TestBindingTransitionRejectsIllegalEdgeAndStaleExpectedState(t *testing.T) {
	now := time.Now().UTC()
	binding := RoleBinding{ID: "binding-a03-negative", PrincipalID: "agent-1", Role: "developer", ScopeID: "task:1", BoundBy: "admin", BoundAt: now, PolicyDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", State: StateBound}
	if _, err := TransitionBinding(context.Background(), binding, BindingTransition{Actor: "admin", From: StateUnbound, To: StateRevoked}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale/illegal edge err=%v", err)
	}
}
