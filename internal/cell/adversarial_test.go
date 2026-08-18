package cell

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type denyingCellAuthorizer struct{ err error }

func (a denyingCellAuthorizer) AuthorizeCellPrepare(context.Context, Spec) error { return a.err }

func TestA04PrepareFailsClosedBeforeBackendOrPersistence(t *testing.T) {
	repository := &memoryCellRepository{}
	backend := &countingCellBackend{}
	manager := NewManager(repository, map[BackendKind]Backend{BackendNative: backend})
	_, err := manager.Prepare(context.Background(), Spec{TaskID: "TASK-cell-a04", Workspace: "/tmp/cell-a04", Backend: BackendNative})
	if !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("missing authorizer error=%v, want ErrAuthorizationDenied", err)
	}
	if repository.record.ID != "" || backend.prepareCalls != 0 {
		t.Fatalf("side effects record=%+v prepare_calls=%d, want zero", repository.record, backend.prepareCalls)
	}
}

func TestA04AuthorizationFailureDoesNotLeakInput(t *testing.T) {
	marker := "MARSHAL_TEST_SECRET_T02_A04"
	repository := &memoryCellRepository{}
	backend := &countingCellBackend{}
	manager := NewManager(repository, map[BackendKind]Backend{BackendNative: backend}, denyingCellAuthorizer{err: errors.New(marker)})
	_, err := manager.Prepare(context.Background(), Spec{TaskID: "TASK-cell-a04", Workspace: "/tmp/cell-a04", Backend: BackendNative})
	if !errors.Is(err, ErrAuthorizationDenied) || strings.Contains(err.Error(), marker) {
		t.Fatalf("authorization error=%v, want stable redacted denial", err)
	}
	if repository.record.ID != "" || backend.prepareCalls != 0 {
		t.Fatalf("side effects record=%+v prepare_calls=%d, want zero", repository.record, backend.prepareCalls)
	}
}

type denyingCellSecretBroker struct{}

func (denyingCellSecretBroker) AuthorizeCellSecretRefs(context.Context, TaskID, []SecretRef) error {
	return errors.New("secret broker denied")
}

func TestA07RequestedSecretRefsRequireTheSecretBroker(t *testing.T) {
	repository := &memoryCellRepository{}
	backend := &countingCellBackend{}
	manager := NewManager(repository, map[BackendKind]Backend{BackendNative: backend}, allowingCellAuthorizer{})
	_, err := manager.Prepare(context.Background(), Spec{
		TaskID: "TASK-cell-a07", Workspace: "/tmp/cell-a07", Backend: BackendNative,
		SecretRefs: []SecretRef{"env:API_TOKEN"},
	})
	if !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("secret request error=%v, want ErrAuthorizationDenied", err)
	}
	if repository.record.ID != "" || backend.prepareCalls != 0 {
		t.Fatalf("side effects record=%+v prepare_calls=%d, want zero", repository.record, backend.prepareCalls)
	}
}

func TestA07SecureBackendNeverSilentlyFallsBack(t *testing.T) {
	repository := &memoryCellRepository{}
	manager := NewManager(repository, map[BackendKind]Backend{BackendNative: &countingCellBackend{}}, allowingCellAuthorizer{})
	_, err := manager.Prepare(context.Background(), Spec{TaskID: "TASK-cell-a07", Workspace: "/tmp/cell-a07", Backend: BackendBubblewrap})
	if !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("missing secure backend error=%v, want ErrBackendUnavailable", err)
	}
	if repository.record.ID != "" {
		t.Fatalf("record=%+v, want no mutation", repository.record)
	}
}

func TestA07ForeignHandleCannotExecuteOrDestroy(t *testing.T) {
	repository := &memoryCellRepository{record: Record{ID: "cell-a07", TaskID: "TASK-cell-a07", Backend: BackendNative, Workspace: "/tmp/cell-a07", SpecDigest: "sha256:cell", State: StateRunning}}
	backend := &countingCellBackend{}
	manager := NewManager(repository, map[BackendKind]Backend{BackendNative: backend}, allowingCellAuthorizer{})
	foreign := Handle{ID: "cell-a07", TaskID: "TASK-foreign", Backend: BackendNative, Workspace: "/tmp/cell-a07"}
	if _, err := manager.Exec(context.Background(), foreign, ExecRequest{Command: []string{"true"}}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("foreign exec error=%v, want ErrNotReady", err)
	}
	if err := manager.Destroy(context.Background(), foreign); !errors.Is(err, ErrNotReady) {
		t.Fatalf("foreign destroy error=%v, want ErrNotReady", err)
	}
	if backend.destroyCalls != 0 {
		t.Fatalf("destroy calls=%d, want zero", backend.destroyCalls)
	}
}
