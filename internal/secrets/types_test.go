package secrets

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSecretContractTypesValidateAndExposeStableErrors(t *testing.T) {
	ref := Ref{Provider: "env", Name: "API_TOKEN", Version: "v1"}
	lease := Lease{
		ID:        "lease-1",
		Subject:   "agent-1",
		TaskID:    "task-1",
		Ref:       ref,
		Purpose:   "deploy",
		ExpiresAt: time.Unix(2, 0).UTC(),
	}
	if err := ref.Validate(); err != nil {
		t.Fatalf("valid ref rejected: %v", err)
	}
	if err := lease.Validate(); err != nil {
		t.Fatalf("valid lease rejected: %v", err)
	}
	if !errors.Is(ErrDenied, NewError(CodeDenied, errors.New("secret-value"))) {
		t.Fatal("stable secret error identity is not preserved")
	}
	if got := NewError(CodeProviderFailed, errors.New("secret-value")).Error(); got != ErrProviderFailed.Error() {
		t.Fatalf("unsafe provider error message = %q", got)
	}
}

func TestSecretContractDeclaresNarrowProviderAndBrokerAPIs(t *testing.T) {
	var _ Provider = providerFunc(func(context.Context, Ref) ([]byte, error) { return nil, nil })
	var _ Broker = brokerStub{}
}

func TestSecretContractRejectsMalformedReferencesAndLeases(t *testing.T) {
	badRefs := []Ref{{}, {Provider: "env", Name: "", Version: "v1"}, {Provider: "env", Name: "TOKEN\x00", Version: "v1"}}
	for _, ref := range badRefs {
		if err := ref.Validate(); !errors.Is(err, ErrNotFound) {
			t.Fatalf("ref=%#v error=%v, want ErrNotFound", ref, err)
		}
	}
	if err := (Lease{ID: "lease-1", Subject: "agent-1", TaskID: "task-1", Ref: Ref{Provider: "env", Name: "TOKEN", Version: "v1"}, Purpose: "deploy"}).Validate(); !errors.Is(err, ErrDenied) {
		t.Fatalf("incomplete lease error=%v, want ErrDenied", err)
	}
}

func TestSecretErrorsNeverExposeProviderCause(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T21_A01"
	for _, code := range []ErrorCode{CodeDenied, CodeNotFound, CodeLeaseExpired, CodePurposeMismatch, CodeProviderFailed} {
		err := NewError(code, errors.New(marker))
		if strings.Contains(err.Error(), marker) {
			t.Fatalf("code %s leaked provider cause", code)
		}
	}
}

type providerFunc func(context.Context, Ref) ([]byte, error)

func (providerFunc) Resolve(context.Context, Ref) ([]byte, error) { return nil, nil }

type brokerStub struct{}

func (brokerStub) Lease(context.Context, LeaseRequest) (Lease, error)          { return Lease{}, nil }
func (brokerStub) WithSecret(context.Context, Lease, func([]byte) error) error { return nil }
func (brokerStub) Revoke(context.Context, RevokeRequest) error                 { return nil }
