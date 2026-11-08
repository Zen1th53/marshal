//go:build !governed

package cell

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func approvedBackendSpec(t *testing.T) Spec {
	t.Helper()
	return Spec{TaskID: "approved-task", Workspace: t.TempDir(), Backend: BackendBubblewrap, NetworkProfile: "deny-by-default", Capabilities: []string{"approved-verification"}}
}

func TestApprovedCheckBackendExecutesOnlyExactBoundCheck(t *testing.T) {
	spec := approvedBackendSpec(t)
	const command = "test -f README.md"
	executions, settlements := 0, 0
	backend, err := NewApprovedCheckBackend(spec, command, func(_ context.Context, h Handle, argv []string) (ExecResult, error) {
		executions++
		if h.Workspace != spec.Workspace || !reflect.DeepEqual(argv, []string{"/bin/sh", "-c", command}) {
			t.Fatal("approved identity or command changed")
		}
		argv[2] = "untrusted callback mutation"
		return ExecResult{ExitCode: 7, Stdout: []byte("check failed")}, nil
	}, func(context.Context, Handle) error { settlements++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	spec.Capabilities[0] = "changed after construction"
	original := spec
	original.Capabilities = []string{"approved-verification"}
	handle, err := backend.Prepare(t.Context(), original)
	if err != nil {
		t.Fatal(err)
	}
	id, err := CellIdentity(original)
	if err != nil || handle.ID != id {
		t.Fatal("backend identity differs from durable manager identity")
	}
	for range 2 {
		result, err := backend.Exec(t.Context(), handle, ExecRequest{Command: []string{"/bin/sh", "-c", command}})
		if err != nil || result.ExitCode != 7 {
			t.Fatalf("failed check became success: %+v %v", result, err)
		}
	}
	if err := backend.Destroy(t.Context(), handle); err != nil {
		t.Fatal(err)
	}
	if executions != 2 || settlements != 1 {
		t.Fatal("unexpected execution or settlement callbacks")
	}
}

func TestApprovedCheckBackendRefusesScopeCommandAndEnvironmentChanges(t *testing.T) {
	spec := approvedBackendSpec(t)
	calls := 0
	backend, err := NewApprovedCheckBackend(spec, "true", func(context.Context, Handle, []string) (ExecResult, error) { calls++; return ExecResult{}, nil }, func(context.Context, Handle) error { calls++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	handle, err := backend.Prepare(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	changed := spec
	changed.Capabilities = []string{"different approval"}
	if _, err := backend.Prepare(t.Context(), changed); err == nil {
		t.Fatal("changed approved specification accepted")
	}
	for _, modify := range []func(*Handle){
		func(h *Handle) { h.ID = "foreign-cell" },
		func(h *Handle) { h.TaskID = "foreign-task" },
		func(h *Handle) { h.Workspace = t.TempDir() },
		func(h *Handle) { h.Backend = BackendNative },
	} {
		foreign := handle
		modify(&foreign)
		if _, err := backend.Exec(t.Context(), foreign, ExecRequest{Command: []string{"/bin/sh", "-c", "true"}}); err == nil {
			t.Fatal("foreign handle executed")
		}
		if err := backend.Destroy(t.Context(), foreign); err == nil {
			t.Fatal("foreign handle destroyed")
		}
	}
	for _, req := range []ExecRequest{
		{Command: []string{"/bin/sh", "-c", "true; touch unexpected"}},
		{Command: []string{"true"}},
		{Command: []string{"/bin/sh", "-c", "true"}, Env: map[string]string{"PATH": "/foreign"}},
	} {
		if _, err := backend.Exec(t.Context(), handle, req); err == nil {
			t.Fatal("unapproved request accepted")
		}
	}
	if calls != 0 {
		t.Fatal("refused input reached executor or cleanup")
	}
}

func TestApprovedCheckBackendRequiresSupportedIsolationAndSettlement(t *testing.T) {
	spec := approvedBackendSpec(t)
	execute := func(context.Context, Handle, []string) (ExecResult, error) { return ExecResult{}, nil }
	settle := func(context.Context, Handle) error { return nil }
	for _, change := range []func(*Spec){
		func(s *Spec) { s.Backend = BackendNative },
		func(s *Spec) { s.CPUQuota = 1 },
		func(s *Spec) { s.MemoryBytes = 1024 },
		func(s *Spec) { s.SecretRefs = []SecretRef{"credential"} },
		func(s *Spec) { s.NetworkProfile = "unrestricted" },
	} {
		unsupported := spec
		change(&unsupported)
		if _, err := NewApprovedCheckBackend(unsupported, "true", execute, settle); err == nil {
			t.Fatal("unsupported isolation/resource claim accepted")
		}
	}
	if _, err := NewApprovedCheckBackend(spec, "true", nil, settle); err == nil {
		t.Fatal("missing sandbox executor accepted")
	}
	if _, err := NewApprovedCheckBackend(spec, "true", execute, nil); err == nil {
		t.Fatal("missing settlement proof accepted")
	}
	if _, err := NewApprovedCheckBackend(spec, "", execute, settle); err == nil {
		t.Fatal("empty approved check accepted")
	}
	backend, err := NewApprovedCheckBackend(spec, "true", execute, func(context.Context, Handle) error { return errors.New("owned child settlement unknown") })
	if err != nil {
		t.Fatal(err)
	}
	handle, _ := backend.Prepare(t.Context(), spec)
	if !errors.Is(backend.Destroy(t.Context(), handle), ErrCleanupFailed) {
		t.Fatal("missing settlement was represented as destruction")
	}
}

func TestApprovedCheckBackendCancelledContextCannotInvokeCallbacks(t *testing.T) {
	spec := approvedBackendSpec(t)
	calls := 0
	backend, err := NewApprovedCheckBackend(spec, "true", func(context.Context, Handle, []string) (ExecResult, error) { calls++; return ExecResult{}, nil }, func(context.Context, Handle) error { calls++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	handle, _ := backend.Prepare(t.Context(), spec)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := backend.Prepare(ctx, spec); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := backend.Exec(ctx, handle, ExecRequest{Command: []string{"/bin/sh", "-c", "true"}}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := backend.Destroy(ctx, handle); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("cancelled request invoked callback")
	}
}
