package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/dag"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT29A03StoreTransitionIsCASIdempotentAndTerminalSafe(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutDAGNode(ctx, dag.Node{TaskID: "TASK-A", Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
		t.Fatal(err)
	}
	ready, err := st.TransitionDAGNode(ctx, "TASK-A", dag.StatusPending, dag.StatusReady)
	if err != nil || ready.Status != dag.StatusReady {
		t.Fatalf("pending->ready node=%+v err=%v", ready, err)
	}
	retry, err := st.TransitionDAGNode(ctx, "TASK-A", dag.StatusPending, dag.StatusReady)
	if err != nil || retry.Status != dag.StatusReady {
		t.Fatalf("idempotent retry node=%+v err=%v", retry, err)
	}
	if _, err := st.TransitionDAGNode(ctx, "TASK-A", dag.StatusPending, dag.StatusRunning); !errors.Is(err, dag.ErrInvalidNode) {
		t.Fatalf("illegal transition error=%v want=%v", err, dag.ErrInvalidNode)
	}
	if _, err := st.TransitionDAGNode(ctx, "TASK-A", dag.StatusReady, dag.StatusRunning); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TransitionDAGNode(ctx, "TASK-A", dag.StatusRunning, dag.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TransitionDAGNode(ctx, "TASK-A", dag.StatusRunning, dag.StatusFailed); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale terminal overwrite error=%v want conflict", err)
	}
	loaded, err := st.GetDAGNode(ctx, "TASK-A")
	if err != nil || loaded.Status != dag.StatusCompleted {
		t.Fatalf("terminal node=%+v err=%v", loaded, err)
	}
}

func TestT29A03EngineUnlocksChildOnlyFromCanonicalParentState(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	engine, err := newAuthorizedDAGEngine(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []dag.TaskID{"TASK-PARENT", "TASK-CHILD"} {
		if _, err := engine.AddNode(ctx, dag.AddNodeRequest{RequestID: dag.RequestID("REQ-" + string(id)), Node: dag.Node{TaskID: id, Kind: dag.NodeKindTask, Status: dag.StatusPending}}); err != nil {
			t.Fatalf("AddNode(%s): %v", id, err)
		}
	}
	if _, err := engine.AddEdge(ctx, dag.AddEdgeRequest{RequestID: "REQ-EDGE", Edge: dag.Edge{From: "TASK-PARENT", To: "TASK-CHILD", Condition: dag.ConditionCompleted}}); err != nil {
		t.Fatal(err)
	}
	before, err := engine.Ready(ctx, "TASK-CHILD")
	if err != nil {
		t.Fatal(err)
	}
	if before.Ready || len(before.BlockedBy) != 1 || before.BlockedBy[0] != "TASK-PARENT" {
		t.Fatalf("before readiness=%+v", before)
	}
	if _, err := engine.Transition(ctx, "TASK-PARENT", dag.StatusPending, dag.StatusReady); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Transition(ctx, "TASK-PARENT", dag.StatusReady, dag.StatusRunning); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Transition(ctx, "TASK-PARENT", dag.StatusRunning, dag.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	after, err := engine.Ready(ctx, "TASK-CHILD")
	if err != nil {
		t.Fatal(err)
	}
	if !after.Ready || len(after.BlockedBy) != 0 {
		t.Fatalf("after readiness=%+v", after)
	}
	readyChild, err := engine.Transition(ctx, "TASK-CHILD", dag.StatusPending, dag.StatusReady)
	if err != nil || readyChild.Status != dag.StatusReady {
		t.Fatalf("child transition=%+v err=%v", readyChild, err)
	}
}

func TestT29A03FailedRequiredParentKeepsChildBlocked(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	engine, err := newAuthorizedDAGEngine(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []dag.TaskID{"TASK-PARENT", "TASK-CHILD"} {
		if _, err := engine.AddNode(ctx, dag.AddNodeRequest{RequestID: dag.RequestID("REQ-" + string(id)), Node: dag.Node{TaskID: id, Kind: dag.NodeKindTask, Status: dag.StatusPending}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := engine.AddEdge(ctx, dag.AddEdgeRequest{RequestID: "REQ-EDGE", Edge: dag.Edge{From: "TASK-PARENT", To: "TASK-CHILD", Condition: dag.ConditionCompleted}}); err != nil {
		t.Fatal(err)
	}
	for _, step := range [][2]dag.NodeStatus{{dag.StatusPending, dag.StatusReady}, {dag.StatusReady, dag.StatusRunning}, {dag.StatusRunning, dag.StatusFailed}} {
		if _, err := engine.Transition(ctx, "TASK-PARENT", step[0], step[1]); err != nil {
			t.Fatal(err)
		}
	}
	got, err := engine.Ready(ctx, "TASK-CHILD")
	if err != nil {
		t.Fatal(err)
	}
	if got.Ready || len(got.BlockedBy) != 1 || got.BlockedBy[0] != "TASK-PARENT" {
		t.Fatalf("readiness=%+v", got)
	}
	if _, err := engine.Transition(ctx, "TASK-CHILD", dag.StatusPending, dag.StatusReady); !errors.Is(err, dag.ErrInvalidNode) {
		t.Fatalf("blocked child transition error=%v", err)
	}
}

func TestT29A03EngineRejectsCycleWithoutPersistingEdge(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	engine, _ := newAuthorizedDAGEngine(st)
	for _, id := range []dag.TaskID{"TASK-A", "TASK-B"} {
		if _, err := engine.AddNode(ctx, dag.AddNodeRequest{RequestID: dag.RequestID("REQ-" + string(id)), Node: dag.Node{TaskID: id, Kind: dag.NodeKindTask, Status: dag.StatusPending}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := engine.AddEdge(ctx, dag.AddEdgeRequest{RequestID: "REQ-AB", Edge: dag.Edge{From: "TASK-A", To: "TASK-B", Condition: dag.ConditionCompleted}}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.AddEdge(ctx, dag.AddEdgeRequest{RequestID: "REQ-BA", Edge: dag.Edge{From: "TASK-B", To: "TASK-A", Condition: dag.ConditionCompleted}}); !errors.Is(err, dag.ErrCycle) {
		t.Fatalf("cycle error=%v want=%v", err, dag.ErrCycle)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM dag_edges"); got != 1 {
		t.Fatalf("edges=%d want=1", got)
	}
}

func TestT29A03TopologicalOrderStableForDiamond(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	engine, _ := newAuthorizedDAGEngine(st)
	nodes := []dag.Node{
		{TaskID: "TASK-A", Kind: dag.NodeKindTask, Status: dag.StatusPending, Priority: 10},
		{TaskID: "TASK-B", Kind: dag.NodeKindTask, Status: dag.StatusPending, Priority: 5},
		{TaskID: "TASK-C", Kind: dag.NodeKindTask, Status: dag.StatusPending, Priority: 5},
		{TaskID: "TASK-D", Kind: dag.NodeKindTask, Status: dag.StatusPending, Priority: 1},
	}
	for _, node := range nodes {
		if _, err := engine.AddNode(ctx, dag.AddNodeRequest{RequestID: dag.RequestID("REQ-" + string(node.TaskID)), Node: node}); err != nil {
			t.Fatal(err)
		}
	}
	for i, edge := range []dag.Edge{
		{From: "TASK-A", To: "TASK-B", Condition: dag.ConditionCompleted},
		{From: "TASK-A", To: "TASK-C", Condition: dag.ConditionCompleted},
		{From: "TASK-B", To: "TASK-D", Condition: dag.ConditionCompleted},
		{From: "TASK-C", To: "TASK-D", Condition: dag.ConditionCompleted},
	} {
		if _, err := engine.AddEdge(ctx, dag.AddEdgeRequest{RequestID: dag.RequestID(fmt.Sprintf("REQ-E-%d", i)), Edge: edge}); err != nil {
			t.Fatal(err)
		}
	}
	for run := 0; run < 5; run++ {
		order, err := engine.Topological(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := []dag.TaskID{"TASK-A", "TASK-B", "TASK-C", "TASK-D"}
		if len(order) != len(want) {
			t.Fatalf("order=%+v", order)
		}
		for i := range want {
			if order[i].TaskID != want[i] {
				t.Fatalf("run=%d order=%+v", run, order)
			}
		}
	}
}

func TestT29A03CanAddChildWhileParentRunningButCannotRewriteCompletedTarget(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	engine, _ := newAuthorizedDAGEngine(st)
	for _, id := range []dag.TaskID{"TASK-PARENT", "TASK-CHILD", "TASK-HISTORY"} {
		if _, err := engine.AddNode(ctx, dag.AddNodeRequest{RequestID: dag.RequestID("REQ-" + string(id)), Node: dag.Node{TaskID: id, Kind: dag.NodeKindTask, Status: dag.StatusPending}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, step := range [][2]dag.NodeStatus{{dag.StatusPending, dag.StatusReady}, {dag.StatusReady, dag.StatusRunning}} {
		if _, err := engine.Transition(ctx, "TASK-PARENT", step[0], step[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := engine.AddEdge(ctx, dag.AddEdgeRequest{RequestID: "REQ-DYNAMIC", Edge: dag.Edge{From: "TASK-PARENT", To: "TASK-CHILD", Condition: dag.ConditionCompleted}}); err != nil {
		t.Fatalf("add child during parent execution: %v", err)
	}
	for _, step := range [][2]dag.NodeStatus{{dag.StatusPending, dag.StatusReady}, {dag.StatusReady, dag.StatusRunning}, {dag.StatusRunning, dag.StatusCompleted}} {
		if _, err := engine.Transition(ctx, "TASK-HISTORY", step[0], step[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := engine.AddEdge(ctx, dag.AddEdgeRequest{RequestID: "REQ-REWRITE", Edge: dag.Edge{From: "TASK-PARENT", To: "TASK-HISTORY", Condition: dag.ConditionCompleted}}); !errors.Is(err, dag.ErrInvalidNode) {
		t.Fatalf("completed history rewrite error=%v", err)
	}
}

func TestT29A03ExactEdgeRetryReconcilesAfterTargetBecomesReady(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	engine, _ := newAuthorizedDAGEngine(st)
	for _, id := range []dag.TaskID{"TASK-P", "TASK-C"} {
		if _, err := engine.AddNode(ctx, dag.AddNodeRequest{RequestID: dag.RequestID("REQ-" + string(id)), Node: dag.Node{TaskID: id, Kind: dag.NodeKindTask, Status: dag.StatusPending}}); err != nil {
			t.Fatal(err)
		}
	}
	request := dag.AddEdgeRequest{RequestID: "REQ-EDGE", Edge: dag.Edge{From: "TASK-P", To: "TASK-C", Condition: dag.ConditionCompleted}}
	first, err := engine.AddEdge(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range [][2]dag.NodeStatus{{dag.StatusPending, dag.StatusReady}, {dag.StatusReady, dag.StatusRunning}, {dag.StatusRunning, dag.StatusCompleted}} {
		if _, err := engine.Transition(ctx, "TASK-P", step[0], step[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := engine.Transition(ctx, "TASK-C", dag.StatusPending, dag.StatusReady); err != nil {
		t.Fatal(err)
	}
	retry, err := engine.AddEdge(ctx, request)
	if err != nil || retry != first {
		t.Fatalf("retry=%+v first=%+v err=%v", retry, first, err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM dag_edges"); got != 1 {
		t.Fatalf("edges=%d", got)
	}
}

func TestT29A03CancelledMutationHasNoSideEffectAndSafeError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st := openTestStore(t)
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	engine, _ := newAuthorizedDAGEngine(st)
	const marker = "MARSHAL_TEST_SECRET_T29_A03_5b91"
	_, err := engine.AddNode(ctx, dag.AddNodeRequest{RequestID: marker, Node: dag.Node{TaskID: "TASK-CANCEL", Kind: dag.NodeKindTask, Status: dag.StatusPending}})
	if err == nil {
		t.Fatal("cancelled mutation succeeded")
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatal("cancelled mutation leaked request marker")
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM dag_nodes"); got != 0 {
		t.Fatalf("nodes=%d want=0", got)
	}
}

func TestT29A03ReadyStatusCannotOverrideUnsatisfiedCanonicalDependency(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutDAGNode(ctx, dag.Node{TaskID: "TASK-P", Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutDAGNode(ctx, dag.Node{TaskID: "TASK-C", Kind: dag.NodeKindTask, Status: dag.StatusReady}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutDAGEdge(ctx, dag.Edge{From: "TASK-P", To: "TASK-C", Condition: dag.ConditionCompleted}); err != nil {
		t.Fatal(err)
	}
	engine, _ := newAuthorizedDAGEngine(st)
	got, err := engine.Ready(ctx, "TASK-C")
	if err != nil {
		t.Fatal(err)
	}
	if got.Ready || len(got.BlockedBy) != 1 || got.BlockedBy[0] != "TASK-P" {
		t.Fatalf("readiness trusted stale status instead of dependencies: %+v", got)
	}
}

func TestT29A03CompetingTerminalTransitionsHaveOneCanonicalWinner(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutDAGNode(ctx, dag.Node{TaskID: "TASK-RACE", Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
		t.Fatal(err)
	}
	for _, step := range [][2]dag.NodeStatus{{dag.StatusPending, dag.StatusReady}, {dag.StatusReady, dag.StatusRunning}} {
		if _, err := st.TransitionDAGNode(ctx, "TASK-RACE", step[0], step[1]); err != nil {
			t.Fatal(err)
		}
	}
	type result struct {
		status dag.NodeStatus
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, target := range []dag.NodeStatus{dag.StatusCompleted, dag.StatusFailed} {
		target := target
		go func() {
			<-start
			node, err := st.TransitionDAGNode(ctx, "TASK-RACE", dag.StatusRunning, target)
			results <- result{node.Status, err}
		}()
	}
	close(start)
	var success, conflict int
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err == nil {
			success++
		} else if errors.Is(r.err, model.ErrConflict) {
			conflict++
		} else {
			t.Fatalf("unexpected race result=%+v", r)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
	loaded, err := st.GetDAGNode(ctx, "TASK-RACE")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != dag.StatusCompleted && loaded.Status != dag.StatusFailed {
		t.Fatalf("final=%+v", loaded)
	}
}

func TestT29A03TransitionAndReadinessSurviveRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "dag-a03.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	engine, _ := newAuthorizedDAGEngine(first)
	for _, id := range []dag.TaskID{"TASK-P", "TASK-C"} {
		if _, err := engine.AddNode(ctx, dag.AddNodeRequest{RequestID: dag.RequestID("REQ-" + string(id)), Node: dag.Node{TaskID: id, Kind: dag.NodeKindTask, Status: dag.StatusPending}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := engine.AddEdge(ctx, dag.AddEdgeRequest{RequestID: "REQ-E", Edge: dag.Edge{From: "TASK-P", To: "TASK-C", Condition: dag.ConditionCompleted}}); err != nil {
		t.Fatal(err)
	}
	for _, step := range [][2]dag.NodeStatus{{dag.StatusPending, dag.StatusReady}, {dag.StatusReady, dag.StatusRunning}, {dag.StatusRunning, dag.StatusCompleted}} {
		if _, err := engine.Transition(ctx, "TASK-P", step[0], step[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	engine2, _ := newAuthorizedDAGEngine(second)
	got, err := engine2.Ready(ctx, "TASK-C")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Ready || len(got.BlockedBy) != 0 {
		t.Fatalf("restart readiness=%+v", got)
	}
}
