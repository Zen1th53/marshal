package dag

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
)

func TestT29A07DiamondReadinessUsesOnlyCanonicalParentState(t *testing.T) {
	backend := newA05Backend()
	for _, id := range []TaskID{"TASK-A", "TASK-B", "TASK-C", "TASK-D"} {
		backend.nodes[id] = Node{TaskID: id, Kind: NodeKindTask, Status: StatusPending}
	}
	backend.edges = []Edge{
		{From: "TASK-A", To: "TASK-B", Condition: ConditionCompleted},
		{From: "TASK-A", To: "TASK-C", Condition: ConditionCompleted},
		{From: "TASK-B", To: "TASK-D", Condition: ConditionCompleted},
		{From: "TASK-C", To: "TASK-D", Condition: ConditionCompleted},
	}
	engine := a05Engine(t, backend, &a05EventSink{})

	got, err := engine.Ready(context.Background(), "TASK-D")
	if err != nil {
		t.Fatal(err)
	}
	if got.Ready || fmt.Sprint(got.BlockedBy) != "[TASK-B TASK-C]" {
		t.Fatalf("initial readiness=%+v", got)
	}
	// Free-form / provider prose has no input channel into readiness. Only
	// canonical node state can satisfy the machine condition.
	backend.nodes["TASK-B"] = Node{TaskID: "TASK-B", Kind: NodeKindTask, Status: StatusCompleted}
	got, err = engine.Ready(context.Background(), "TASK-D")
	if err != nil {
		t.Fatal(err)
	}
	if got.Ready || fmt.Sprint(got.BlockedBy) != "[TASK-C]" {
		t.Fatalf("partial readiness=%+v", got)
	}
	backend.nodes["TASK-C"] = Node{TaskID: "TASK-C", Kind: NodeKindTask, Status: StatusCompleted}
	got, err = engine.Ready(context.Background(), "TASK-D")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Ready || len(got.BlockedBy) != 0 {
		t.Fatalf("final readiness=%+v", got)
	}
}

func TestT29A07DynamicChildCanBeAddedWhileParentRunningWithoutHistoryRewrite(t *testing.T) {
	backend := newA05Backend()
	backend.nodes["TASK-PARENT"] = Node{TaskID: "TASK-PARENT", Kind: NodeKindTask, Status: StatusRunning}
	sink := &a05EventSink{}
	engine := a05Engine(t, backend, sink)

	if _, err := engine.AddNode(context.Background(), AddNodeRequest{RequestID: "REQ-A07-CHILD", Node: Node{TaskID: "TASK-CHILD", Kind: NodeKindTask, Status: StatusPending}}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.AddEdge(context.Background(), AddEdgeRequest{RequestID: "REQ-A07-EDGE", Edge: Edge{From: "TASK-PARENT", To: "TASK-CHILD", Condition: ConditionCompleted}}); err != nil {
		t.Fatal(err)
	}
	got, err := engine.Ready(context.Background(), "TASK-CHILD")
	if err != nil {
		t.Fatal(err)
	}
	if got.Ready || fmt.Sprint(got.BlockedBy) != "[TASK-PARENT]" {
		t.Fatalf("readiness=%+v", got)
	}
	if backend.nodes["TASK-PARENT"].Status != StatusRunning {
		t.Fatalf("parent history rewritten: %+v", backend.nodes["TASK-PARENT"])
	}
}

func TestT29A07FailedRequiredParentBlocksCompletedCondition(t *testing.T) {
	backend := newA05Backend()
	backend.nodes["TASK-PARENT"] = Node{TaskID: "TASK-PARENT", Kind: NodeKindTask, Status: StatusFailed}
	backend.nodes["TASK-CHILD"] = Node{TaskID: "TASK-CHILD", Kind: NodeKindTask, Status: StatusPending}
	backend.edges = []Edge{{From: "TASK-PARENT", To: "TASK-CHILD", Condition: ConditionCompleted}}
	engine := a05Engine(t, backend, &a05EventSink{})
	got, err := engine.Ready(context.Background(), "TASK-CHILD")
	if err != nil {
		t.Fatal(err)
	}
	if got.Ready || len(got.BlockedBy) != 1 || got.BlockedBy[0] != "TASK-PARENT" {
		t.Fatalf("readiness=%+v", got)
	}
}

func TestT29A07CompletedHistoryCannotGainNewInboundDependency(t *testing.T) {
	backend := newA05Backend()
	backend.nodes["TASK-PARENT"] = Node{TaskID: "TASK-PARENT", Kind: NodeKindTask, Status: StatusCompleted}
	backend.nodes["TASK-DONE"] = Node{TaskID: "TASK-DONE", Kind: NodeKindTask, Status: StatusCompleted}
	engine := a05Engine(t, backend, &a05EventSink{})
	_, err := engine.AddEdge(context.Background(), AddEdgeRequest{RequestID: "REQ-A07-HISTORY", Edge: Edge{From: "TASK-PARENT", To: "TASK-DONE", Condition: ConditionCompleted}})
	if !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("AddEdge() error=%v want=%v", err, ErrInvalidNode)
	}
	if len(backend.edges) != 0 {
		t.Fatalf("edge mutated completed history: %+v", backend.edges)
	}
}

func FuzzT29A07EdgeValidationNeverAcceptsInvalidIdentityOrCondition(f *testing.F) {
	seeds := [][3]string{{"TASK-A", "TASK-B", "completed"}, {"TASK-A", "TASK-A", "completed"}, {"bad", "TASK-B", "completed"}, {"TASK-A", "TASK-B", "agent-says-done"}}
	for _, s := range seeds {
		f.Add(s[0], s[1], s[2])
	}
	f.Fuzz(func(t *testing.T, from, to, condition string) {
		edge := Edge{From: TaskID(from), To: TaskID(to), Condition: EdgeCondition(condition)}
		err := edge.Validate()
		accepted := validTaskID(edge.From) && validTaskID(edge.To) && edge.From != edge.To && edge.Condition.Valid()
		if accepted && err != nil {
			t.Fatalf("valid edge rejected: %+v err=%v", edge, err)
		}
		if !accepted && err == nil {
			t.Fatalf("invalid edge accepted: %+v", edge)
		}
	})
}

func FuzzT29A07ReadinessBlockedByOrderIsDeterministic(f *testing.F) {
	f.Add("TASK-C,TASK-A,TASK-B")
	f.Fuzz(func(t *testing.T, encoded string) {
		parts := []TaskID{}
		seen := map[TaskID]bool{}
		start := 0
		for i := 0; i <= len(encoded); i++ {
			if i != len(encoded) && encoded[i] != ',' {
				continue
			}
			id := TaskID(encoded[start:i])
			start = i + 1
			if !validTaskID(id) || seen[id] || id == "TASK-CHILD" {
				continue
			}
			seen[id] = true
			parts = append(parts, id)
			if len(parts) == 16 {
				break
			}
		}
		backend := newA05Backend()
		backend.nodes["TASK-CHILD"] = Node{TaskID: "TASK-CHILD", Kind: NodeKindTask, Status: StatusPending}
		for _, id := range parts {
			backend.nodes[id] = Node{TaskID: id, Kind: NodeKindTask, Status: StatusPending}
			backend.edges = append(backend.edges, Edge{From: id, To: "TASK-CHILD", Condition: ConditionCompleted})
		}
		engine, err := NewEngine(backend)
		if err != nil {
			t.Fatal(err)
		}
		got, err := engine.Ready(context.Background(), "TASK-CHILD")
		if err != nil {
			t.Fatal(err)
		}
		want := append([]TaskID(nil), parts...)
		sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
		if fmt.Sprint(got.BlockedBy) != fmt.Sprint(want) {
			t.Fatalf("blocked=%v want=%v", got.BlockedBy, want)
		}
	})
}

func TestT29A07AttackMatrixRejectsMalformedMutations(t *testing.T) {
	baseNode := Node{TaskID: "TASK-GOOD", Kind: NodeKindTask, Status: StatusPending}
	baseEdge := Edge{From: "TASK-A", To: "TASK-B", Condition: ConditionCompleted}
	cases := []struct {
		name string
		node *Node
		edge *Edge
	}{
		{"node-empty-id", &Node{TaskID: "", Kind: NodeKindTask, Status: StatusPending}, nil},
		{"node-prefix", &Node{TaskID: "BAD", Kind: NodeKindTask, Status: StatusPending}, nil},
		{"node-control", &Node{TaskID: "TASK-A\nB", Kind: NodeKindTask, Status: StatusPending}, nil},
		{"node-invalid-utf8", &Node{TaskID: TaskID(string([]byte{'T', 'A', 'S', 'K', '-', 0xff})), Kind: NodeKindTask, Status: StatusPending}, nil},
		{"node-kind-empty", &Node{TaskID: "TASK-A", Kind: "", Status: StatusPending}, nil},
		{"node-kind-provider", &Node{TaskID: "TASK-A", Kind: "provider", Status: StatusPending}, nil},
		{"node-status-empty", &Node{TaskID: "TASK-A", Kind: NodeKindTask, Status: ""}, nil},
		{"node-status-prose", &Node{TaskID: "TASK-A", Kind: NodeKindTask, Status: "agent-says-ready"}, nil},
		{"edge-empty-from", nil, &Edge{From: "", To: "TASK-B", Condition: ConditionCompleted}},
		{"edge-empty-to", nil, &Edge{From: "TASK-A", To: "", Condition: ConditionCompleted}},
		{"edge-bad-from", nil, &Edge{From: "A", To: "TASK-B", Condition: ConditionCompleted}},
		{"edge-bad-to", nil, &Edge{From: "TASK-A", To: "B", Condition: ConditionCompleted}},
		{"edge-self-completed", nil, &Edge{From: "TASK-A", To: "TASK-A", Condition: ConditionCompleted}},
		{"edge-self-failed", nil, &Edge{From: "TASK-A", To: "TASK-A", Condition: ConditionFailed}},
		{"edge-self-blocked", nil, &Edge{From: "TASK-A", To: "TASK-A", Condition: ConditionBlocked}},
		{"edge-self-skipped", nil, &Edge{From: "TASK-A", To: "TASK-A", Condition: ConditionSkipped}},
		{"edge-condition-empty", nil, &Edge{From: "TASK-A", To: "TASK-B", Condition: ""}},
		{"edge-condition-ready", nil, &Edge{From: "TASK-A", To: "TASK-B", Condition: "ready"}},
		{"edge-condition-running", nil, &Edge{From: "TASK-A", To: "TASK-B", Condition: "running"}},
		{"edge-condition-provider", nil, &Edge{From: "TASK-A", To: "TASK-B", Condition: "provider-approved"}},
		{"edge-from-control", nil, &Edge{From: "TASK-A\t", To: "TASK-B", Condition: ConditionCompleted}},
		{"edge-to-control", nil, &Edge{From: "TASK-A", To: "TASK-B\r", Condition: ConditionCompleted}},
		{"edge-from-invalid-utf8", nil, &Edge{From: TaskID(string([]byte{'T', 'A', 'S', 'K', '-', 0xfe})), To: "TASK-B", Condition: ConditionCompleted}},
		{"edge-to-invalid-utf8", nil, &Edge{From: "TASK-A", To: TaskID(string([]byte{'T', 'A', 'S', 'K', '-', 0xfd})), Condition: ConditionCompleted}},
		{"edge-from-space", nil, &Edge{From: "TASK A", To: "TASK-B", Condition: ConditionCompleted}},
		{"edge-to-space", nil, &Edge{From: "TASK-A", To: "TASK B", Condition: ConditionCompleted}},
		{"edge-from-slash", nil, &Edge{From: "TASK-/A", To: "TASK-B", Condition: ConditionCompleted}},
		{"edge-to-slash", nil, &Edge{From: "TASK-A", To: "TASK-/B", Condition: ConditionCompleted}},
		{"edge-from-colon", nil, &Edge{From: "TASK-A:B", To: "TASK-B", Condition: ConditionCompleted}},
		{"edge-to-colon", nil, &Edge{From: "TASK-A", To: "TASK-B:B", Condition: ConditionCompleted}},
		{"edge-from-unicode", nil, &Edge{From: "TASK-☃", To: "TASK-B", Condition: ConditionCompleted}},
		{"edge-to-unicode", nil, &Edge{From: "TASK-A", To: "TASK-☃", Condition: ConditionCompleted}},
		{"node-id-space", &Node{TaskID: " TASK-A", Kind: NodeKindTask, Status: StatusPending}, nil},
		{"node-id-trailing-space", &Node{TaskID: "TASK-A ", Kind: NodeKindTask, Status: StatusPending}, nil},
		{"node-id-slash", &Node{TaskID: "TASK-A/B", Kind: NodeKindTask, Status: StatusPending}, nil},
		{"node-id-colon", &Node{TaskID: "TASK-A:B", Kind: NodeKindTask, Status: StatusPending}, nil},
	}
	if len(cases) < 30 {
		t.Fatalf("attack matrix too small: %d", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.node != nil {
				n := *tc.node
				if n == (Node{}) {
					n = baseNode
				}
				if err := n.Validate(); err == nil {
					t.Fatalf("malformed node accepted: %+v", n)
				}
				return
			}
			e := *tc.edge
			if e == (Edge{}) {
				e = baseEdge
			}
			if err := e.Validate(); err == nil {
				t.Fatalf("malformed edge accepted: %+v", e)
			}
		})
	}
}

func FuzzT29A07CycleInsertionDetectedForArbitraryChainLength(f *testing.F) {
	f.Add(uint8(2))
	f.Add(uint8(8))
	f.Add(uint8(32))
	f.Fuzz(func(t *testing.T, raw uint8) {
		length := int(raw%48) + 2
		backend := newA05Backend()
		ids := make([]TaskID, length)
		for i := 0; i < length; i++ {
			id := TaskID(fmt.Sprintf("TASK-FUZZ-%02d", i))
			ids[i] = id
			backend.nodes[id] = Node{TaskID: id, Kind: NodeKindTask, Status: StatusPending}
			if i > 0 {
				backend.edges = append(backend.edges, Edge{From: ids[i-1], To: id, Condition: ConditionCompleted})
			}
		}
		engine, err := NewEngine(backend)
		if err != nil {
			t.Fatal(err)
		}
		cycle, err := engine.wouldCycle(context.Background(), Edge{From: ids[length-1], To: ids[0], Condition: ConditionCompleted})
		if err != nil {
			t.Fatal(err)
		}
		if !cycle {
			t.Fatalf("failed to detect cycle for chain length %d", length)
		}
		noncycle, err := engine.wouldCycle(context.Background(), Edge{From: ids[0], To: ids[length-1], Condition: ConditionCompleted})
		if err != nil {
			t.Fatal(err)
		}
		if noncycle {
			t.Fatalf("false cycle for forward edge length %d", length)
		}
	})
}
