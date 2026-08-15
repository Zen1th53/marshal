package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/dag"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestRuntimeClaimRejectsDAGManagedTaskUntilCanonicalReady(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })

	agent, err := runtime.RegisterAgent(context.Background(), RegisterAgentRequest{Name: "dag-agent", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportTasks(context.Background(), []model.Task{{ID: "TASK-DAG-A06", Title: "dag gated", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.store.PutDAGNode(context.Background(), dag.Node{TaskID: "TASK-DAG-PARENT", Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.store.PutDAGNode(context.Background(), dag.Node{TaskID: "TASK-DAG-A06", Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.store.PutDAGEdge(context.Background(), dag.Edge{From: "TASK-DAG-PARENT", To: "TASK-DAG-A06", Condition: dag.ConditionCompleted}); err != nil {
		t.Fatal(err)
	}

	_, err = runtime.Claim(context.Background(), ClaimRequest{TaskID: "TASK-DAG-A06", AgentID: agent.ID, ExpectedRevision: 0})
	if !errors.Is(err, dag.ErrNotReady) {
		t.Fatalf("Claim() error = %v, want %v", err, dag.ErrNotReady)
	}
	if got, countErr := runtime.store.Count(context.Background(), "sessions"); countErr != nil || got != 0 {
		t.Fatalf("sessions=%d err=%v, want zero side effect", got, countErr)
	}

	if _, err := runtime.store.TransitionDAGNode(context.Background(), "TASK-DAG-PARENT", dag.StatusPending, dag.StatusReady); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.store.TransitionDAGNode(context.Background(), "TASK-DAG-PARENT", dag.StatusReady, dag.StatusRunning); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.store.TransitionDAGNode(context.Background(), "TASK-DAG-PARENT", dag.StatusRunning, dag.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	claim, err := runtime.Claim(context.Background(), ClaimRequest{TaskID: "TASK-DAG-A06", AgentID: agent.ID, ExpectedRevision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if claim.Lease.TaskID != "TASK-DAG-A06" {
		t.Fatalf("claim=%+v", claim)
	}
}

type dagA06Adapter struct{ calls int }

func (a *dagA06Adapter) Probe(context.Context) (adapter.Probe, error) {
	a.calls++
	return adapter.Probe{Name: "fake", Available: true, Version: "1"}, nil
}
func (a *dagA06Adapter) Run(context.Context, adapter.Request) (adapter.Result, error) {
	a.calls++
	return adapter.Result{Status: adapter.StatusSuccess}, nil
}
func (a *dagA06Adapter) Status(context.Context, string) (adapter.Status, error) {
	return adapter.StatusSuccess, nil
}
func (a *dagA06Adapter) Resume(context.Context, string, adapter.Request) (adapter.Result, error) {
	a.calls++
	return adapter.Result{Status: adapter.StatusSuccess}, nil
}
func (a *dagA06Adapter) Capabilities() map[string]string               { return nil }
func (a *dagA06Adapter) CollectEvidence(adapter.Result) map[string]any { return nil }
func (a *dagA06Adapter) Shutdown(context.Context, string) error        { return nil }

func TestRuntimeRunDAGGateDominatesAllProviderAdapters(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	adapters := map[string]adapter.Adapter{}
	fakes := map[string]*dagA06Adapter{}
	for _, name := range []string{"codex", "claude", "gemini", "opencode"} {
		fake := &dagA06Adapter{}
		fakes[name] = fake
		adapters[name] = fake
	}
	runtime, err := OpenWithOptions(context.Background(), repo.Path(), Options{Adapters: adapters})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	agent, err := runtime.RegisterAgent(context.Background(), RegisterAgentRequest{Name: "dag-provider-agent", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportTasks(context.Background(), []model.Task{{ID: "TASK-DAG-PROVIDERS", Title: "blocked", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.store.PutDAGNode(context.Background(), dag.Node{TaskID: "TASK-DAG-BLOCKER", Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.store.PutDAGNode(context.Background(), dag.Node{TaskID: "TASK-DAG-PROVIDERS", Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.store.PutDAGEdge(context.Background(), dag.Edge{From: "TASK-DAG-BLOCKER", To: "TASK-DAG-PROVIDERS", Condition: dag.ConditionCompleted}); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"codex", "claude", "gemini", "opencode"} {
		_, err := runtime.Run(context.Background(), RunRequest{TaskID: "TASK-DAG-PROVIDERS", AgentID: agent.ID, Adapter: name, ExpectedRevision: 0})
		if !errors.Is(err, dag.ErrNotReady) {
			t.Fatalf("provider %s error=%v", name, err)
		}
		if fakes[name].calls != 0 {
			t.Fatalf("provider %s calls=%d, want zero", name, fakes[name].calls)
		}
	}
	if got, err := runtime.store.Count(context.Background(), "sessions"); err != nil || got != 0 {
		t.Fatalf("sessions=%d err=%v", got, err)
	}
}

func TestRuntimeDAGGateSurvivesRestartAndDoesNotLeakTaskTitle(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	agent, err := runtime.RegisterAgent(context.Background(), RegisterAgentRequest{Name: "dag-restart-agent", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	const marker = "MARSHAL_TEST_SECRET_T29_A06_73c1"
	if _, err := runtime.ImportTasks(context.Background(), []model.Task{{ID: "TASK-DAG-RESTART", Title: marker, Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.store.PutDAGNode(context.Background(), dag.Node{TaskID: "TASK-DAG-RESTART-BLOCKER", Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.store.PutDAGNode(context.Background(), dag.Node{TaskID: "TASK-DAG-RESTART", Kind: dag.NodeKindTask, Status: dag.StatusPending}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.store.PutDAGEdge(context.Background(), dag.Edge{From: "TASK-DAG-RESTART-BLOCKER", To: "TASK-DAG-RESTART", Condition: dag.ConditionCompleted}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	runtime, err = Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	_, err = runtime.Claim(context.Background(), ClaimRequest{TaskID: "TASK-DAG-RESTART", AgentID: agent.ID, ExpectedRevision: 0})
	if !errors.Is(err, dag.ErrNotReady) {
		t.Fatalf("error=%v", err)
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("secret title leaked in error: %q", err.Error())
	}
}
