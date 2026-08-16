package secrets

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSecretValidationRejectsControlAndOversizedIdentity(t *testing.T) {
	for _, ref := range []Ref{
		{Provider: "env\n", Name: "TOKEN", Version: "v1"},
		{Provider: "env", Name: strings.Repeat("A", 1025), Version: "v1"},
	} {
		if err := ref.Validate(); !errors.Is(err, ErrNotFound) {
			t.Fatalf("ref=%#v error=%v, want ErrNotFound", ref, err)
		}
	}
	lease := Lease{ID: "lease", Subject: "agent", TaskID: "task", Ref: Ref{Provider: "env", Name: "TOKEN", Version: "v1"}, Purpose: "deploy\nlog", IssuedAt: time.Unix(1, 0), ExpiresAt: time.Unix(2, 0)}
	if err := lease.Validate(); !errors.Is(err, ErrDenied) {
		t.Fatalf("lease error=%v, want ErrDenied", err)
	}
}

func TestSecretLeaseCannotBeRetargetedAcrossTaskOrReference(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	store := &memoryLeaseStore{}
	engine, err := NewEngine(EngineConfig{
		Store: store, Capability: allowSecretCapability{},
		Providers: map[string]Provider{"env": providerFunc(func(context.Context, Ref) ([]byte, error) { return []byte("secret"), nil })},
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engine.Lease(context.Background(), LeaseRequest{ID: "bound", Subject: "agent", TaskID: "task-a", Ref: Ref{Provider: "env", Name: "TOKEN", Version: "v1"}, Purpose: "test", IssuedAt: now, ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	lease.TaskID = "task-b"
	if err := engine.WithSecret(context.Background(), lease, func([]byte) error { t.Fatal("retargeted callback invoked"); return nil }); !errors.Is(err, ErrDenied) {
		t.Fatalf("retarget error=%v, want ErrDenied", err)
	}
}

func TestSecretMarkerNeverEntersLifecycleEventsOrSafeError(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T21_A07_UNIQUE"
	now := time.Unix(100, 0).UTC()
	eventStore := &memoryEventStore{}
	engine, err := NewEngine(EngineConfig{
		Store: &memoryLeaseStore{}, Capability: allowSecretCapability{}, EventStore: eventStore,
		Providers: map[string]Provider{"env": providerFunc(func(context.Context, Ref) ([]byte, error) { return []byte(marker), nil })},
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engine.Lease(context.Background(), LeaseRequest{ID: "marker", Subject: "agent", TaskID: "task", Ref: Ref{Provider: "env", Name: "TOKEN", Version: "v1"}, Purpose: "test", IssuedAt: now, ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.WithSecret(context.Background(), lease, func(value []byte) error {
		if string(value) != marker {
			t.Fatalf("callback value=%q", value)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join([]string{fmt.Sprint(eventStore.events), ErrDenied.Error(), ErrProviderFailed.Error()}, "\n"), marker) {
		t.Fatal("secret marker leaked into event or safe error")
	}
}

func FuzzRefValidationNeverPanics(f *testing.F) {
	f.Add("env", "TOKEN", "v1")
	f.Add("env\n", "TOKEN", "v1")
	f.Fuzz(func(t *testing.T, provider, name, version string) {
		_ = (Ref{Provider: provider, Name: name, Version: version}).Validate()
	})
}
