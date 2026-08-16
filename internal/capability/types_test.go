package capability

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCapabilityContractProvidesTypedGrantDecisionAndErrors(t *testing.T) {
	issued := time.Unix(100, 0).UTC()
	grant := Grant{
		ID:        "cap-1",
		Subject:   "agent-1",
		TaskID:    "task-1",
		Kind:      KindFilesystemRead,
		Scope:     Scope{Resource: "/workspace", Actions: []string{"read"}},
		IssuedAt:  issued,
		ExpiresAt: issued.Add(time.Hour),
		Issuer:    "broker-admin",
		State:     GrantActive,
	}
	if err := grant.Validate(); err != nil {
		t.Fatalf("valid grant rejected: %v", err)
	}

	decision := Decision{Allowed: false, Reason: ReasonDenied, GrantID: grant.ID}
	if err := decision.Validate(); err != nil {
		t.Fatalf("valid deny decision rejected: %v", err)
	}
	if !errors.Is(ErrDenied, ErrCapability) {
		t.Fatal("typed capability errors must support errors.Is")
	}

	var broker Broker = contractProbe{}
	if broker == nil {
		t.Fatal("broker contract must accept an implementation")
	}
	_ = context.Background()
}

func TestCapabilityContractRejectsUnknownKindInvalidScopeAndAllowedDeny(t *testing.T) {
	if (Kind("filesystem.read")).Valid() {
		t.Fatal("unknown capability kind accepted")
	}
	if err := (Scope{Resource: "/workspace"}).Validate(); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("invalid scope error = %v", err)
	}
	if err := (Decision{Allowed: true, Reason: ReasonDenied}).Validate(); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("allowed deny decision error = %v", err)
	}
}

type contractProbe struct{}

func (contractProbe) Grant(context.Context, GrantRequest) (Grant, error) { return Grant{}, nil }
func (contractProbe) Authorize(context.Context, Query) (Decision, error) { return Decision{}, nil }
func (contractProbe) Revoke(context.Context, RevokeRequest) error        { return nil }
