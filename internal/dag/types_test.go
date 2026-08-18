package dag

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNodeContractValidatesClosedIdentityAndEnums(t *testing.T) {
	node := Node{TaskID: "TASK-build", Kind: NodeKindTask, Status: StatusPending, Priority: 10}
	if err := node.Validate(); err != nil {
		t.Fatalf("valid node rejected: %v", err)
	}

	for _, candidate := range []Node{
		{TaskID: "build", Kind: NodeKindTask, Status: StatusPending},
		{TaskID: "TASK-build", Kind: NodeKind("provider"), Status: StatusPending},
		{TaskID: "TASK-build", Kind: NodeKindTask, Status: NodeStatus("unknown")},
	} {
		if err := candidate.Validate(); !errors.Is(err, ErrInvalidNode) {
			t.Fatalf("invalid node error = %v, want %v", err, ErrInvalidNode)
		}
	}
}

func TestEdgeConditionRejectsUnknownValueAndSelfCycle(t *testing.T) {
	edge := Edge{From: "TASK-A", To: "TASK-B", Condition: ConditionCompleted}
	if err := edge.Validate(); err != nil {
		t.Fatalf("valid edge rejected: %v", err)
	}

	edge.Condition = EdgeCondition("unknown")
	if err := edge.Validate(); !errors.Is(err, ErrInvalidCondition) {
		t.Fatalf("unknown condition error = %v, want %v", err, ErrInvalidCondition)
	}

	edge = Edge{From: "TASK-A", To: "TASK-A", Condition: ConditionCompleted}
	if err := edge.Validate(); !errors.Is(err, ErrCycle) {
		t.Fatalf("self edge error = %v, want %v", err, ErrCycle)
	}
}

func TestMutationRequestsRequireStableRequestIdentity(t *testing.T) {
	add := AddNodeRequest{RequestID: "REQ-1", Node: Node{TaskID: "TASK-A", Kind: NodeKindTask, Status: StatusPending}}
	if err := add.Validate(); err != nil {
		t.Fatalf("valid add-node request rejected: %v", err)
	}
	add.RequestID = ""
	if err := add.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty request error = %v, want %v", err, ErrInvalidRequest)
	}
}

func TestReadinessCloneDoesNotAliasBlockers(t *testing.T) {
	original := Readiness{TaskID: "TASK-B", Ready: false, BlockedBy: []TaskID{"TASK-A"}}
	clone := CloneReadiness(original)
	clone.BlockedBy[0] = "TASK-X"
	if original.BlockedBy[0] != "TASK-A" {
		t.Fatalf("clone mutated original: %+v", original)
	}
}

func TestDAGErrorIsMachineReadableAndSecretSafe(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T29_A01_X7"
	cause := errors.New(marker)
	err := NewError(CodeCycle, cause)
	if !errors.Is(err, cause) || !errors.Is(err, ErrCycle) {
		t.Fatalf("error lost cause or stable identity: %v", err)
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("public error leaked marker: %q", err.Error())
	}
	if ReasonCode(err) != CodeCycle {
		t.Fatalf("ReasonCode = %q, want %q", ReasonCode(err), CodeCycle)
	}
}

func compileGraph(g Graph, ctx context.Context) {
	_, _ = g.AddNode(ctx, AddNodeRequest{})
	_, _ = g.AddEdge(ctx, AddEdgeRequest{})
	_, _ = g.Ready(ctx, "TASK-A")
	_, _ = g.Topological(ctx)
}
