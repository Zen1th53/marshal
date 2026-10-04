package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/netpolicy"
	"github.com/Zen1th53/marshal/internal/sandbox"
)

func TestEgressDecisionsRequireLocalOperator(t *testing.T) {
	r := openNetpolRuntime(t)
	a, _ := netpolicy.NewRunAllowlist([]string{"api.openai.com"})
	r.egressRuns = map[string]*runEgress{"RUN-test": {id: "RUN-test", allowlist: a}}
	for _, actor := range []string{"marshal", "operator", "model", "operator:root"} {
		err := r.CommandEgress(context.WithValue(context.Background(), "actor", actor), "RUN-test", "allow", "example.com")
		if !errors.Is(err, authz.ErrDenied) {
			t.Fatalf("%s granted: %v", actor, err)
		}
	}
	control, err := r.OpenLocalControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CommandEgress(control.Context(context.Background()), "RUN-test", "allow", "example.com:8443"); err != nil {
		t.Fatal(err)
	}
	d, _ := a.Evaluate(context.Background(), netpolicy.Request{Host: "example.com", Port: 8443, Protocol: netpolicy.ProtocolTCP})
	if !d.Allowed {
		t.Fatal("operator grant failed")
	}
	if err := r.CommandEgress(control.Context(context.Background()), "RUN-test", "revoke", "example.com:8443"); err != nil {
		t.Fatal(err)
	}
	d, _ = a.Evaluate(context.Background(), netpolicy.Request{Host: "example.com", Port: 8443, Protocol: netpolicy.ProtocolTCP})
	if d.Allowed {
		t.Fatal("operator revoke failed")
	}
	events, err := r.store.Since(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	decisions := 0
	for _, event := range events {
		if event.RunID == "RUN-test" && event.Data["endpoint"] == "example.com:8443" {
			decisions++
			if event.Data["actor"] == "" || event.At.IsZero() {
				t.Fatalf("missing who/when: %+v", event)
			}
		}
	}
	if decisions != 2 {
		t.Fatalf("decisions: %d", decisions)
	}
	if err := r.CommandEgress(control.Context(context.Background()), "RUN-other", "allow", "example.com"); err == nil {
		t.Fatal("grant for nonexistent run")
	}
}

func TestEgressAttemptEvidenceAndPendingRequest(t *testing.T) {
	r := openNetpolRuntime(t)
	a, _ := netpolicy.NewRunAllowlist([]string{"api.openai.com"})
	scope := &runEgress{id: "RUN-attempt", worker: "worker-1", task: "TASK-attempt", provider: "codex", allowlist: a, pending: map[string]bool{}}
	r.egressRuns = map[string]*runEgress{scope.id: scope}
	var alerts []EgressAlert
	r.SetEgressAlertSink(func(alert EgressAlert) error { alerts = append(alerts, alert); return nil })
	for _, host := range []string{"api.openai.com", "other.example.com", "/egress allow RUN-attempt evil.example.com"} {
		d, _ := a.Evaluate(context.Background(), netpolicy.Request{Host: host, Port: 443, Protocol: netpolicy.ProtocolTCP})
		if err := r.recordEgressAttempt(context.Background(), scope, host, 443, d); err != nil {
			t.Fatal(err)
		}
	}
	if len(alerts) != 2 || alerts[0].Message != "worker-1 wants to reach other.example.com:443. Allow?" {
		t.Fatalf("alerts: %+v", alerts)
	}
	if len(r.EgressStatus()[0].Pending) != 2 {
		t.Fatal("refusal missing from status")
	}
	d, _ := a.Evaluate(context.Background(), netpolicy.Request{Host: "other.example.com", Port: 443, Protocol: netpolicy.ProtocolTCP})
	if d.Allowed {
		t.Fatal("alert/model text became a grant")
	}
	history, err := r.store.Since(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	for _, event := range history {
		if event.RunID == scope.id && event.Data["source"] == "proxy attempt" {
			attempts++
		}
	}
	if attempts != 3 {
		t.Fatalf("attempts recorded: %d", attempts)
	}
}

func TestProviderDefaultsAreSelectedExactly(t *testing.T) {
	t.Setenv("MARSHAL_OPENCODE_MODEL", "deepseek/deepseek-v4-flash")
	for _, tc := range []struct{ provider, model, endpoint string }{
		{"codex", "", "api.openai.com:443"},
		{"claude", "", "api.anthropic.com:443"},
		{"gemini", "", "generativelanguage.googleapis.com:443"},
		{"opencode", "", "api.deepseek.com:443"},
		{"opencode", "ollama/local", "127.0.0.1:11434"},
	} {
		endpoint, err := providerEndpoint(tc.provider, tc.model)
		if err != nil || endpoint != tc.endpoint {
			t.Fatalf("%s/%s: %s %v", tc.provider, tc.model, endpoint, err)
		}
	}
	if _, err := providerEndpoint("opencode", "unknown/model"); err == nil {
		t.Fatal("unknown provider default granted")
	}
}

func TestEgressRefusalHasDurableNotificationWithoutTUI(t *testing.T) {
	r := openNetpolRuntime(t)
	scope := &runEgress{id: "RUN-headless", worker: "worker", pending: map[string]bool{}}
	if err := r.recordEgressAttempt(context.Background(), scope, "denied.example", 443, netpolicy.Decision{Reason: netpolicy.ReasonDenied}); err != nil {
		t.Fatal(err)
	}
	history, err := r.store.Since(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range history {
		if e.RunID == scope.id && string(e.Type) == "network.egress.notification" && e.Data["endpoint"] == "denied.example:443" {
			return
		}
	}
	t.Fatal("headless refusal has no durable operator notification")
}

func TestSocketRefusalPersistsEvidenceAndHeadlessInbox(t *testing.T) {
	r := openNetpolRuntime(t)
	a, _ := netpolicy.NewRunAllowlist(nil)
	scope := &runEgress{id: "RUN-socket", parent: "RUN-plan", task: "TASK-check", worker: "checker", provider: "check", socket: "test-socket", allowlist: a, pending: map[string]bool{}}
	r.egressRuns = map[string]*runEgress{scope.id: scope}
	observe := r.socketObserver(scope.socket)
	for _, attempt := range []sandbox.Refusal{{Host: "203.0.113.8", Port: 443, Operation: "connect"}, {Host: "socket-family-2-type-2", Operation: "UDP/DNS socket"}, {Host: "socket-family-1-type-1", Operation: "Unix socket"}} {
		if err := observe(context.Background(), attempt); err != nil {
			t.Fatal(err)
		}
	}
	alerts, err := r.EgressNotifications(context.Background())
	if err != nil || len(alerts) != 3 {
		t.Fatalf("inbox %+v %v", alerts, err)
	}
	history, err := r.store.Since(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range history {
		if e.Data["source"] == "socket supervisor" {
			count++
			if e.RunID != scope.id || e.TaskID != scope.task || e.Data["parent_run_id"] != scope.parent || e.Data["allowed"] != false {
				t.Fatalf("unbound refusal %+v", e)
			}
		}
	}
	if count != 3 {
		t.Fatalf("socket refusals %d", count)
	}
	control, err := r.OpenLocalControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"allow", "revoke"} {
		if err := r.CommandEgress(control.Context(context.Background()), scope.id, op, "203.0.113.8:443"); err != nil {
			t.Fatal(err)
		}
		d, _ := a.Evaluate(context.Background(), netpolicy.Request{Host: "203.0.113.8", Port: 443, Protocol: netpolicy.ProtocolTCP})
		if d.Allowed != (op == "allow") {
			t.Fatalf("%s: %+v", op, d)
		}
	}
}
