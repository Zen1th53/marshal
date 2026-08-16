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
