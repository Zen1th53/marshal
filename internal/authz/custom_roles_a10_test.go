package authz

import (
	"context"
	"testing"
)

func TestConfiguredCustomRoleUsesCanonicalAuthorityEvaluation(t *testing.T) {
	catalog, err := NewRoleCatalog([]Role{{
		Name:        "release-reviewer",
		Authorities: []Authority{AuthorityReleaseApprove},
	}})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	decision, err := catalog.Can(context.Background(), Principal{ID: "agent-custom", Role: Role{Name: "release-reviewer"}}, AuthorityReleaseApprove, "change:1")
	if err != nil || !decision.Allowed || decision.Role != "release-reviewer" {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
}

func TestConfiguredCustomRoleRejectsUnknownAuthority(t *testing.T) {
	if _, err := NewRoleCatalog([]Role{{Name: "unsafe", Authorities: []Authority{"authority.unknown"}}}); err != ErrUnknownAuthority {
		t.Fatalf("err=%v want=%v", err, ErrUnknownAuthority)
	}
}
