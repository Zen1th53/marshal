package dag

import (
	"errors"
	"strings"
	"testing"
)

func TestTaskAndRequestIdentifiersRejectControlAndOversizedInput(t *testing.T) {
	long := "TASK-" + strings.Repeat("a", maxIdentifierBytes)
	for _, id := range []TaskID{"TASK-\nA", TaskID(long)} {
		if err := (Node{TaskID: id, Kind: NodeKindTask, Status: StatusPending}).Validate(); !errors.Is(err, ErrInvalidNode) {
			t.Fatalf("task ID %q error = %v, want invalid node", id, err)
		}
	}
	request := AddNodeRequest{RequestID: RequestID("REQ-\x00"), Node: Node{TaskID: "TASK-A", Kind: NodeKindTask, Status: StatusPending}}
	if err := request.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("control request ID error = %v, want invalid request", err)
	}
}

func TestUnknownDAGErrorHasSafeFallbackAndReason(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T29_A01_UNKNOWN"
	err := NewError(Code("UNKNOWN"), errors.New(marker))
	if ReasonCode(err) != Code("UNKNOWN") {
		t.Fatalf("ReasonCode = %q, want UNKNOWN", ReasonCode(err))
	}
	if err.Error() != "dag operation failed" || strings.Contains(err.Error(), marker) {
		t.Fatalf("unsafe unknown error message: %q", err.Error())
	}
}
