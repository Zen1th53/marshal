package capability

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBrokerContractUsesContextAndStructuredTypes(t *testing.T) {
	var broker Broker = contractProbe{}
	if broker == nil {
		t.Fatal("broker contract must accept an implementation")
	}
}

type contractProbe struct{}

func (contractProbe) Grant(context.Context, GrantRequest) (Grant, error) { return Grant{}, nil }
func (contractProbe) Authorize(context.Context, Query) (Decision, error) { return Decision{}, nil }
func (contractProbe) Revoke(context.Context, RevokeRequest) error        { return nil }

func TestGrantContractValidatesScopedExpiringIdentity(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	grant := Grant{
		ID: "CAP-01", Subject: "AGENT-01", TaskID: "TASK-01", Kind: KindFilesystemWrite,
		Scope:    Scope{Resource: "/workspace/task-01", Actions: []string{"write"}, Constraints: map[string]string{"worktree": "/workspace/task-01"}},
		IssuedAt: now, ExpiresAt: now.Add(time.Hour), Issuer: "AGENT-ADMIN",
	}
	if err := grant.Validate(); err != nil {
		t.Fatalf("valid grant rejected: %v", err)
	}
}

func TestScopeRejectsEmptyResourceAndUnknownKind(t *testing.T) {
	grant := Grant{ID: "CAP-02", Subject: "AGENT-01", TaskID: "TASK-01", Kind: CapabilityKind("arbitrary.privilege"), Scope: Scope{Actions: []string{"write"}}, IssuedAt: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC), Issuer: "AGENT-ADMIN"}
	if err := grant.Validate(); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("Validate() error = %v, want ErrInvalidScope", err)
	}
}

func TestCapabilityErrorsExposeStableSafeCodes(t *testing.T) {
	err := NewError(CodeDenied, "untrusted detail")
	if err.Code != CodeDenied || err.Error() != "capability denied" {
		t.Fatalf("error = %#v", err)
	}
	if !errors.Is(err, ErrDenied) {
		t.Fatal("denied error does not support errors.Is")
	}
}

func TestGrantRequestValidatesTheSameScopedContract(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	request := GrantRequest{Subject: "AGENT-01", TaskID: "TASK-01", Kind: KindFilesystemRead, Scope: Scope{Resource: "/workspace/task-01", Actions: []string{"read"}}, IssuedAt: now, ExpiresAt: now.Add(time.Hour), Issuer: "AGENT-ADMIN"}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid grant request rejected: %v", err)
	}
}

func TestGrantValidationDoesNotExposeUntrustedScopeText(t *testing.T) {
	secret := "MARSHAL_TEST_SECRET_T01_A01"
	grant := Grant{ID: "CAP-03", Subject: "AGENT-01", TaskID: "TASK-01", Kind: KindFilesystemWrite, Scope: Scope{Resource: secret, Actions: []string{"write", "write"}}, IssuedAt: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC), Issuer: "AGENT-ADMIN"}
	err := grant.Validate()
	if !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("Validate() error = %v, want ErrInvalidScope", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("validation error exposed untrusted scope text")
	}
}
