package model_test

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

// TestT80ScopeKindEnum verifies all required scope kinds are defined and valid.
func TestT80ScopeKindEnum(t *testing.T) {
	validScopes := []model.MemoryScopeKind{
		model.ScopeProject,
		model.ScopeTask,
		model.ScopeAgent,
		model.ScopeSession,
		model.ScopeBranch,
		model.ScopeTeam,
		model.ScopeOperatorPrivate,
	}
	for _, s := range validScopes {
		if !s.IsValid() {
			t.Errorf("scope kind %q should be valid", s)
		}
	}
	var zero model.MemoryScopeKind
	if zero.IsValid() {
		t.Error("zero MemoryScopeKind should be invalid")
	}
}

// TestT80ScopeACLForbidsUnknownKind verifies that NewMemoryScope rejects
// unknown scope kinds.
func TestT80ScopeACLForbidsUnknownKind(t *testing.T) {
	_, err := model.NewMemoryScope("unknown_scope", "X")
	if err == nil {
		t.Error("expected error for unknown scope kind")
	}
}

// TestT80CrossProjectBoundaryDenied verifies that a read request with a
// different project ID than the record is denied by the scope check.
func TestT80CrossProjectBoundaryDenied(t *testing.T) {
	scope, err := model.NewMemoryScope(string(model.ScopeProject), "PROJ-A")
	if err != nil {
		t.Fatal(err)
	}
	// Requesting project PROJ-B must not be allowed to read PROJ-A scope.
	if scope.AllowsRead("PROJ-B", "") {
		t.Error("cross-project read must be denied")
	}
	if scope.AllowsRead("PROJ-A", "") {
		// Correct — same project allowed.
	}
}

// TestT80OperatorPrivateDeniedToAgent verifies operator-private scope is
// inaccessible to an agent actor.
func TestT80OperatorPrivateDeniedToAgent(t *testing.T) {
	scope, err := model.NewMemoryScope(string(model.ScopeOperatorPrivate), "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	// An agent (not matching the scope owner) must not see this.
	if scope.AllowsRead("PROJ-A", "agent-xyz") {
		t.Error("operator-private memory must be denied to non-owner agent")
	}
	// The scope owner must be allowed.
	if !scope.AllowsRead("PROJ-A", "operator-1") {
		t.Error("operator-private memory must be allowed to its owner")
	}
}
