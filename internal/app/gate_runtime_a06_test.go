package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/gate"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
)

func TestRuntimeRunAppliesConfiguredGateBeforeClaimOrAdapter(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	fake := &gateRuntimeAdapter{}
	gateEngine, err := gate.NewEngine(gate.EngineConfig{
		PolicyDigest:   policy.PolicyDigest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		RequiredChecks: map[gate.GatePoint][]gate.CheckID{gate.GatePointPreExecution: {"required-check"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := OpenWithOptions(context.Background(), repo.Path(), Options{
		Adapters: map[string]adapter.Adapter{"codex": fake}, GateEngine: gateEngine,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	agent, err := runtime.RegisterAgent(context.Background(), RegisterAgentRequest{Name: "gate-agent", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportTasks(context.Background(), []model.Task{{ID: "TASK-GATE-A06", Title: "gate task", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	_, err = runtime.Run(context.Background(), RunRequest{TaskID: "TASK-GATE-A06", AgentID: agent.ID, Adapter: "codex", ExpectedRevision: 0})
	if !errors.Is(err, gate.ErrUnknownCheck) {
		t.Fatalf("Run error=%v want=%v", err, gate.ErrUnknownCheck)
	}
	if fake.calls != 0 {
		t.Fatalf("adapter calls=%d want=0", fake.calls)
	}
	if got, countErr := runtime.store.Count(context.Background(), "sessions"); countErr != nil || got != 0 {
		t.Fatalf("sessions=%d err=%v want zero", got, countErr)
	}
}

type gateRuntimeAdapter struct{ calls int }

func (a *gateRuntimeAdapter) Probe(context.Context) (adapter.Probe, error) {
	a.calls++
	return adapter.Probe{Available: true, Version: "1"}, nil
}
func (a *gateRuntimeAdapter) Run(context.Context, adapter.Request) (adapter.Result, error) {
	a.calls++
	return adapter.Result{Status: adapter.StatusSuccess}, nil
}
func (a *gateRuntimeAdapter) Status(context.Context, string) (adapter.Status, error) {
	return adapter.StatusSuccess, nil
}
func (a *gateRuntimeAdapter) Resume(context.Context, string, adapter.Request) (adapter.Result, error) {
	a.calls++
	return adapter.Result{Status: adapter.StatusSuccess}, nil
}
func (a *gateRuntimeAdapter) Capabilities() map[string]string               { return nil }
func (a *gateRuntimeAdapter) CollectEvidence(adapter.Result) map[string]any { return nil }
func (a *gateRuntimeAdapter) Shutdown(context.Context, string) error        { return nil }
