package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/netpolicy"
)

func openNetpolRuntime(t *testing.T) *Runtime {
	t.Helper()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	return runtime
}

func allowRule(host string, ports ...int) []netpolicy.Rule {
	return []netpolicy.Rule{{ID: "egress-1", HostPattern: host, Protocol: netpolicy.ProtocolTCP, Ports: ports, Action: netpolicy.ActionAllow}}
}

type netpolMockAdapter struct{ probes, runs int }

func (a *netpolMockAdapter) Probe(context.Context) (adapter.Probe, error) {
	a.probes++
	return adapter.Probe{Available: true, Version: "1.0"}, nil
}
func (a *netpolMockAdapter) Run(ctx context.Context, req adapter.Request) (adapter.Result, error) {
	a.runs++
	_ = os.WriteFile(filepath.Join(req.Worktree, "result.txt"), []byte("egress test result"), 0644)
	return adapter.Result{Status: adapter.StatusSuccess, ExitCode: 0}, nil
}
func (a *netpolMockAdapter) Status(context.Context, string) (adapter.Status, error) {
	return adapter.StatusSuccess, nil
}
func (a *netpolMockAdapter) Resume(context.Context, string, adapter.Request) (adapter.Result, error) {
	return adapter.Result{Status: adapter.StatusSuccess, ExitCode: 0}, nil
}
func (a *netpolMockAdapter) Capabilities() map[string]string               { return nil }
func (a *netpolMockAdapter) CollectEvidence(adapter.Result) map[string]any { return nil }
func (a *netpolMockAdapter) Shutdown(context.Context, string) error        { return nil }

func TestRunWithRestrictedEgressFailsClosedWithoutEnforcingBackend(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	fake := &netpolMockAdapter{}
	runtime, err := OpenWithOptions(context.Background(), repo.Path(), Options{
		Adapters: map[string]adapter.Adapter{"codex": fake},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })

	agent, err := runtime.RegisterAgent(context.Background(), RegisterAgentRequest{Name: "dev-netpol", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportTasks(context.Background(), []model.Task{
		{ID: "TASK-NETPOL-RUN", Title: "restricted egress", Status: model.TaskReady, Risk: model.R1},
	}); err != nil {
		t.Fatal(err)
	}

	before, err := runtime.Task(t.Context(), "TASK-NETPOL-RUN")
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Run(context.Background(), RunRequest{
		TaskID:          "TASK-NETPOL-RUN",
		AgentID:         agent.ID,
		Adapter:         "codex",
		NetworkRequired: true,
		EgressRules:     allowRule("api.example.com", 443),
	})
	if assertLegacyRunFixtureRefused(t, runtime, before, result, err) {
		if fake.probes != 0 || fake.runs != 0 {
			t.Fatal("refused legacy Run called the adapter", fake.probes, fake.runs)
		}
		return
	}
	if !errors.Is(err, netpolicy.ErrEnforcementUnavailable) {
		t.Fatalf("expected ErrEnforcementUnavailable, got %v", err)
	}
}

func TestRunWithNetworkRequiredButEmptyAllowlistDenied(t *testing.T) {
	runtime := openNetpolRuntime(t)
	agent, err := runtime.RegisterAgent(context.Background(), RegisterAgentRequest{Name: "dev-netpol-empty", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportTasks(context.Background(), []model.Task{
		{ID: "TASK-NETPOL-EMPTY", Title: "empty allowlist", Status: model.TaskReady, Risk: model.R1},
	}); err != nil {
		t.Fatal(err)
	}

	before, err := runtime.Task(t.Context(), "TASK-NETPOL-EMPTY")
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Run(context.Background(), RunRequest{
		TaskID:          "TASK-NETPOL-EMPTY",
		AgentID:         agent.ID,
		Adapter:         "codex",
		NetworkRequired: true,
	})
	if assertLegacyRunFixtureRefused(t, runtime, before, result, err) {
		return
	}
	if err == nil {
		t.Fatal("network required without an explicit allowlist was allowed")
	}
	if !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied, got %v", err)
	}
}
