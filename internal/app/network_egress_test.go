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

func TestAuthorizeNetworkEgressAllowlistSemantics(t *testing.T) {
	runtime := openNetpolRuntime(t)
	const agent, session, task = "agent-netpol", "session-netpol", "TASK-NETPOL"

	evalRequest := func(host string, port int) netpolicy.Decision {
		ev, err := authorizeNetworkEgress(runtime.policy, agent, session, task, model.RoleDeveloper, model.R1, allowRule("api.example.com", 443))
		if err != nil {
			t.Fatalf("authorize: %v", err)
		}
		dec, err := ev.Evaluate(context.Background(), netpolicy.Request{SubjectID: agent, TaskID: task, ChangeID: "c1", Host: host, Protocol: netpolicy.ProtocolTCP, Port: port})
		if err != nil {
			t.Fatalf("evaluate %s:%d: %v", host, port, err)
		}
		return dec
	}

	// Allowed endpoint
	if dec := evalRequest("api.example.com", 443); !dec.Allowed {
		t.Fatalf("allowed endpoint denied: %+v", dec)
	}
	// Denied hostname
	if dec := evalRequest("evil.example.com", 443); dec.Allowed {
		t.Fatalf("denied hostname allowed: %+v", dec)
	}
	// Denied port
	if dec := evalRequest("api.example.com", 22); dec.Allowed {
		t.Fatalf("denied port allowed: %+v", dec)
	}

	// Wildcard handling
	ev, err := authorizeNetworkEgress(runtime.policy, agent, session, task, model.RoleDeveloper, model.R1, allowRule("*.example.com", 443))
	if err != nil {
		t.Fatalf("wildcard authorize: %v", err)
	}
	if dec, _ := ev.Evaluate(context.Background(), netpolicy.Request{SubjectID: agent, TaskID: task, ChangeID: "c1", Host: "api.example.com", Protocol: netpolicy.ProtocolTCP, Port: 443}); !dec.Allowed {
		t.Fatalf("wildcard subdomain denied: %+v", dec)
	}
	if dec, _ := ev.Evaluate(context.Background(), netpolicy.Request{SubjectID: agent, TaskID: task, ChangeID: "c1", Host: "example.com", Protocol: netpolicy.ProtocolTCP, Port: 443}); dec.Allowed {
		t.Fatalf("wildcard matched base domain: %+v", dec)
	}

	// IPv4 loopback
	ev4, err := authorizeNetworkEgress(runtime.policy, agent, session, task, model.RoleDeveloper, model.R1, allowRule("127.0.0.1", 11434))
	if err != nil {
		t.Fatalf("ipv4 authorize: %v", err)
	}
	if dec, _ := ev4.Evaluate(context.Background(), netpolicy.Request{SubjectID: agent, TaskID: task, ChangeID: "c1", Host: "127.0.0.1", IP: "127.0.0.1", Protocol: netpolicy.ProtocolTCP, Port: 11434}); !dec.Allowed {
		t.Fatalf("ipv4 loopback denied: %+v", dec)
	}
	// IPv6 loopback
	ev6, err := authorizeNetworkEgress(runtime.policy, agent, session, task, model.RoleDeveloper, model.R1, allowRule("::1", 11434))
	if err != nil {
		t.Fatalf("ipv6 authorize: %v", err)
	}
	if dec, _ := ev6.Evaluate(context.Background(), netpolicy.Request{SubjectID: agent, TaskID: task, ChangeID: "c1", Host: "::1", IP: "::1", Protocol: netpolicy.ProtocolTCP, Port: 11434}); !dec.Allowed {
		t.Fatalf("ipv6 loopback denied: %+v", dec)
	}

	// Malformed rules rejected
	if _, err := authorizeNetworkEgress(runtime.policy, agent, session, task, model.RoleDeveloper, model.R1, []netpolicy.Rule{
		{ID: "bad", HostPattern: "bad host!", Protocol: netpolicy.ProtocolTCP, Ports: []int{443}, Action: netpolicy.ActionAllow},
	}); err == nil {
		t.Fatal("malformed rule was accepted")
	}

	// Empty allowlist is default-deny
	if _, err := authorizeNetworkEgress(runtime.policy, agent, session, task, model.RoleDeveloper, model.R1, nil); err == nil {
		t.Fatal("empty allowlist was accepted")
	}
}

type netpolMockAdapter struct{}

func (a *netpolMockAdapter) Probe(context.Context) (adapter.Probe, error) {
	return adapter.Probe{Available: true, Version: "1.0"}, nil
}
func (a *netpolMockAdapter) Run(ctx context.Context, req adapter.Request) (adapter.Result, error) {
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

	_, err = runtime.Run(context.Background(), RunRequest{
		TaskID:          "TASK-NETPOL-RUN",
		AgentID:         agent.ID,
		Adapter:         "codex",
		NetworkRequired: true,
		EgressRules:     allowRule("api.example.com", 443),
	})
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

	_, err = runtime.Run(context.Background(), RunRequest{
		TaskID:          "TASK-NETPOL-EMPTY",
		AgentID:         agent.ID,
		Adapter:         "codex",
		NetworkRequired: true,
	})
	if err == nil {
		t.Fatal("network required without an explicit allowlist was allowed")
	}
	if !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied, got %v", err)
	}
}
