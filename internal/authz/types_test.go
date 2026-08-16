package authz

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCanAllowsDeclaredAuthorityWithStructuredDecision(t *testing.T) {
	principal := Principal{ID: "agent-1", Role: Role{
		Name: "developer", Authorities: []Authority{AuthoritySourceWrite},
	}}
	decision, err := Can(context.Background(), principal, AuthoritySourceWrite, "worktree:/repo")
	if err != nil {
		t.Fatalf("Can: %v", err)
	}
	if !decision.Allowed || decision.Reason != CodeAllowed || decision.SubjectID != principal.ID || decision.Authority != AuthoritySourceWrite {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestCanRejectsUnknownAuthorityWithoutAllowing(t *testing.T) {
	principal := Principal{ID: "agent-1", Role: Role{Name: "developer", Authorities: []Authority{AuthoritySourceWrite}}}
	decision, err := Can(context.Background(), principal, Authority("secret.admin"), "worktree:/repo")
	if !errors.Is(err, ErrUnknownAuthority) || decision.Allowed || decision.Reason != CodeUnknownAuthority {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
}

func TestRoleValidationRejectsUnknownRoleAndDuplicateAuthorityWithoutSecretLeak(t *testing.T) {
	marker := "MARSHAL_TEST_SECRET_T04_A01_9f2d"
	_, err := Can(context.Background(), Principal{ID: marker, Role: Role{
		Name: "unknown", Authorities: []Authority{AuthoritySourceWrite, AuthoritySourceWrite},
	}}, AuthoritySourceWrite, "worktree:/repo")
	if !errors.Is(err, ErrUnknownRole) || strings.Contains(err.Error(), marker) {
		t.Fatalf("err=%v", err)
	}
}
