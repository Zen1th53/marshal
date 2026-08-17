package cell

import (
	"errors"
	"strings"
	"testing"
)

func TestA01SpecAndBackendContractValidation(t *testing.T) {
	spec, err := NewSpec(Spec{
		TaskID:         TaskID("TASK-cell-a01"),
		Workspace:      "/tmp/marshal-cell-a01",
		Backend:        BackendNative,
		Capabilities:   []string{"filesystem.read"},
		NetworkProfile: "none",
		CPUQuota:       1,
		MemoryBytes:    64 << 20,
	})
	if err != nil {
		t.Fatalf("NewSpec: %v", err)
	}
	if spec.Backend != BackendNative || spec.TaskID != TaskID("TASK-cell-a01") {
		t.Fatalf("spec = %+v", spec)
	}
	if err := (Spec{TaskID: TaskID("TASK-cell-a01"), Workspace: "../escape", Backend: BackendNative}).Validate(); !errors.Is(err, ErrScopeEscape) {
		t.Fatalf("workspace escape error = %v, want ErrScopeEscape", err)
	}
	if err := (Spec{TaskID: TaskID("TASK-cell-a01"), Workspace: "/tmp/work", Backend: BackendKind("unknown")}).Validate(); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("backend error = %v, want ErrBackendUnavailable", err)
	}
}

func TestA01HandleIsBoundToTaskAndBackend(t *testing.T) {
	handle := Handle{ID: CellID("cell-a01"), TaskID: TaskID("TASK-cell-a01"), Backend: BackendBubblewrap, Workspace: "/tmp/work"}
	if err := handle.Validate(); err != nil {
		t.Fatalf("Handle.Validate: %v", err)
	}
	if err := (Handle{ID: CellID("cell-a01"), TaskID: TaskID("TASK-cell-a01"), Backend: BackendNative, Workspace: "../escape"}).Validate(); !errors.Is(err, ErrScopeEscape) {
		t.Fatalf("handle workspace error = %v, want ErrScopeEscape", err)
	}
}

func TestA01ValidationErrorsAreStableAndDoNotEchoInput(t *testing.T) {
	marker := "MARSHAL_TEST_SECRET_T02_A01"
	err := (Spec{TaskID: TaskID("TASK-cell-a01"), Workspace: marker, Backend: BackendNative}).Validate()
	if !errors.Is(err, ErrScopeEscape) || strings.Contains(err.Error(), marker) {
		t.Fatalf("validation error = %v, want stable scope error without input", err)
	}
	if err := (Spec{TaskID: TaskID("TASK-cell-a01"), Workspace: "/tmp/work\x1b", Backend: BackendNative}).Validate(); !errors.Is(err, ErrScopeEscape) {
		t.Fatalf("control-character error = %v, want ErrScopeEscape", err)
	}
}
