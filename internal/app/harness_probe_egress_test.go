package app

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/netpolicy"
	"github.com/Zen1th53/marshal/internal/sandbox"
)

// A real local proxy CONNECT exercises the same refusal callback that produced
// an unnecessary Marshal network Decision after the native Codex verifier.
func TestHarnessProbeOptionalEndpointIsAuditedWithoutPermissionRequest(t *testing.T) {
	r := openNetpolRuntime(t)
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scope := boundCredentialScope{Project: string(r.admittedProjectIdentity), StorageProject: r.ProjectID(), Provider: "codex", Model: "gpt-test", Purpose: "probe", Probe: "PROBE-optional-egress"}
	ctx := withBoundCredentialScope(t.Context(), r, scope)
	lease, _, err := r.boundCredentialLease(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CommandPermission(control.Context(ctx), lease.request, true, "exact local probe"); err != nil {
		t.Fatal(err)
	}
	broker, err := netpolicy.NewCredentialBroker("codex", "gpt-test", "synthetic-probe-key")
	if err != nil {
		t.Fatal(err)
	}
	alerts := make(chan EgressAlert, 2)
	r.SetEgressAlertSink(func(alert EgressAlert) error { alerts <- alert; return nil })
	socket, cleanup, err := r.startRunEgress(ctx, scope.Probe, "", scope.Provider, scope.Probe, "runtime", []string{"api.openai.com:443"}, broker)
	if err != nil {
		t.Fatal(err)
	}
	// Unrelated optional destinations remain nonexpanding in both editions.
	// Provider vendor domains have separate automatic-grant coverage.
	for _, endpoint := range []string{"optional.example:443", "telemetry.example:443"} {
		connectRefused(t, socket, endpoint)
		if r.EgressRequestPending(scope.Probe, endpoint) {
			t.Fatalf("optional probe endpoint became actionable: %s", endpoint)
		}
		select {
		case alert := <-alerts:
			if alert.State != "denied" || alert.Endpoint != endpoint || strings.Contains(alert.Message, "Allow?") {
				t.Fatalf("probe refusal requested network expansion: %+v", alert)
			}
		case <-time.After(time.Second):
			t.Fatalf("probe refusal lost visible diagnostic: %s", endpoint)
		}
	}
	if err := r.socketObserver(socket)(ctx, sandbox.Refusal{Host: "direct.example", Port: 443, Operation: "connect"}); err != nil {
		t.Fatal(err)
	}
	if r.EgressRequestPending(scope.Probe, "direct.example:443") {
		t.Fatal("direct socket refusal became actionable during probe")
	}
	select {
	case alert := <-alerts:
		if alert.State != "denied" || alert.Endpoint != "direct.example:443" || strings.Contains(alert.Message, "Allow?") {
			t.Fatalf("probe direct socket refusal requested expansion: %+v", alert)
		}
	case <-time.After(time.Second):
		t.Fatal("probe direct socket refusal lost diagnostic")
	}
	if err := r.CommandEgress(control.Context(ctx), scope.Probe, "allow", "oaiusercontent.com:443"); err == nil {
		t.Fatal("optional probe endpoint was manually expanded")
	}
	operator, err := Open(ctx, r.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer operator.Close()
	otherControl, err := operator.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := operator.CommandEgress(otherControl.Context(ctx), scope.Probe, "allow", "oaiusercontent.com:443"); err == nil {
		t.Fatal("shared-store operator expanded exact probe from another runtime")
	}
	history, err := r.store.Since(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	for _, event := range history {
		if event.RunID == scope.Probe && event.Type == events.EventTypeNetworkEgressAttempt && event.Data["source"] == "proxy attempt" {
			if event.Data["allowed"] != false {
				t.Fatalf("optional endpoint audit was not denied: %+v", event)
			}
			attempts++
		}
	}
	if attempts != 2 {
		t.Fatalf("refused CONNECT audit lost: %d attempts", attempts)
	}
	cleanup()
	for _, alert := range mustEgressNotifications(t, r) {
		if alert.RunID == scope.Probe && alert.State != "denied" {
			t.Fatalf("completed probe left orphan permission request: %+v", alert)
		}
	}
}

func TestHarnessProbeLocalProviderAndUntrustedMarkers(t *testing.T) {
	r := openNetpolRuntime(t)
	probe := boundCredentialScope{Project: string(r.admittedProjectIdentity), StorageProject: r.ProjectID(), Provider: "opencode", Model: "ollama/local", Purpose: "probe", Probe: "PROBE-local"}
	ctx := withBoundCredentialScope(t.Context(), r, probe)
	socket, cleanup, err := r.startRunEgress(ctx, probe.Probe, "", probe.Provider, probe.Probe, "runtime", []string{"127.0.0.1:11434"})
	if err != nil {
		t.Fatal(err)
	}
	connectRefused(t, socket, "optional.example:443")
	if r.EgressRequestPending(probe.Probe, "optional.example:443") {
		t.Fatal("local provider probe requested network expansion")
	}
	cleanup()
	for _, alert := range mustEgressNotifications(t, r) {
		if alert.RunID == probe.Probe && alert.State != "denied" {
			t.Fatalf("local probe left an orphan request: %+v", alert)
		}
	}
	for _, scenario := range []struct {
		name, id string
		ctx      context.Context
	}{
		{"missing", "PROBE-missing", t.Context()},
		{"wrong-probe", "PROBE-wrong", ctx},
		{"legacy-marker-alone", "PROBE-legacy", markHarnessVerification(t.Context())},
		{"ordinary-run", "RUN-regular", t.Context()},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			alerts := make(chan EgressAlert, 1)
			r.SetEgressAlertSink(func(alert EgressAlert) error { alerts <- alert; return nil })
			socket, cleanup, err := r.startRunEgress(scenario.ctx, scenario.id, "", probe.Provider, scenario.id, "runtime", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			connectRefused(t, socket, "optional.example:443")
			if !r.EgressRequestPending(scenario.id, "optional.example:443") {
				t.Fatal("nonprobe run suppressed ordinary network request")
			}
			select {
			case alert := <-alerts:
				if alert.State != "waiting" || alert.RunID != scenario.id {
					t.Fatalf("ordinary network alert changed: %+v", alert)
				}
			case <-time.After(time.Second):
				t.Fatal("ordinary network alert lost")
			}
		})
	}
}

func connectRefused(t *testing.T, socket, endpoint string) {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", endpoint, endpoint); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil || response.StatusCode == http.StatusOK {
		t.Fatalf("endpoint was not refused: %s, %+v, %v", endpoint, response, err)
	}
}

func mustEgressNotifications(t *testing.T, r *Runtime) []EgressAlert {
	t.Helper()
	alerts, err := r.EgressNotifications(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return alerts
}
