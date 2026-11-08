package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/netpolicy"
)

// Exercise the production evaluator and durable projection without sockets or
// external traffic, so both editions can be checked in restricted sandboxes.
func testProviderEgressAutomaticDecisions(t *testing.T, automatic bool, probes ...bool) {
	t.Helper()
	probe := len(probes) > 0 && probes[0]
	for _, tc := range []struct {
		provider, host string
		eligible       bool
	}{
		{"codex", "ab.chatgpt.com", true},
		{"codex", "cdn.oaiusercontent.com", true},
		{"codex", "oaistatic.com", true},
		{"claude", "statsig.anthropic.com", true},
		{"claude", "http-intake.logs.datadoghq.com", true},
		{"agy", "daily-cloudcode-pa.googleapis.com", true},
		{"opencode", "opencode.ai", true},
		{"opencode", "api.deepseek.com", true},
		{"codex", "proxy.golang.org", true},
		{"codex", "storage.googleapis.com", true},
		{"codex", "dl.google.com", true},
		{"claude", "registry.npmjs.org", true},
		{"opencode", "files.pythonhosted.org", true},
		{"agy", "static.rust-lang.org", true},
		{"codex", "AB.CHATGPT.COM.", true},
		{"codex", "unrelated.example", false},
		{"codex", "chatgpt.com.evil.example", false},
		{"codex", "evilchatgpt.com", false},
		{"codex", "api.anthropic.com", false},
		{"claude", "ab.chatgpt.com", false},
		{"check", "proxy.golang.org", false},
		{"unregistered", "ab.chatgpt.com", false},
	} {
		t.Run(tc.provider+"/"+tc.host, func(t *testing.T) {
			r := openNetpolRuntime(t)
			list, err := netpolicy.NewRunAllowlist(nil)
			if err != nil {
				t.Fatal(err)
			}
			scope := &runEgress{id: "RUN-provider-auto", provider: tc.provider, harnessProbe: probe, worker: "registered-worker", task: "TASK-provider", socket: filepath.Join(t.TempDir(), "proxy.sock"), allowlist: list, pending: map[string]bool{}}
			r.egressRuns = map[string]*runEgress{scope.id: scope}
			if err := r.recordEgress(t.Context(), scope, events.EventTypeNetworkEgressRequested, map[string]any{"source": "run scope", "allowed_endpoints": []string{}, "harness_probe": probe}); err != nil {
				t.Fatal(err)
			}
			evaluator := &storedRunEgress{runtime: r, scope: scope}
			request := netpolicy.Request{Host: tc.host, Port: 443, Protocol: netpolicy.ProtocolTCP}
			// The private edition also grants every other TCP destination
			// automatically; a harness probe stays nonexpanding.
			open := automatic && openEgressEdition && !probe
			want := automatic && tc.eligible || open
			source := "extreme provider domains"
			if !tc.eligible {
				source = "extreme open egress"
			}
			for range 2 {
				d, err := evaluator.Evaluate(t.Context(), request)
				if err != nil || d.Allowed != want {
					t.Fatalf("decision %+v, err %v; want allowed %v", d, err, want)
				}
				if err := r.recordEgressAttempt(t.Context(), scope, tc.host, 443, d); err != nil {
					t.Fatal(err)
				}
			}
			history, err := r.store.EgressControlEvents(t.Context(), scope.id, 0)
			if err != nil {
				t.Fatal(err)
			}
			grants, notifications := 0, 0
			for _, event := range history {
				if event.Type == events.EventTypeNetworkEgressGranted {
					grants++
					if event.Data["source"] != source || event.Data["provider"] != tc.provider || event.Data["scope_socket"] != scope.socket || event.Data["reason"] == "" || event.Data["allowed"] != true {
						t.Fatalf("incomplete automatic decision evidence: %+v", event)
					}
				}
				if event.Type == events.EventTypeNetworkEgressNotification {
					notifications++
				}
			}
			if want && (grants != 1 || notifications != 0) || !want && (grants != 0 || notifications != 2) {
				t.Fatalf("grants %d, notifications %d, automatic %v", grants, notifications, want)
			}
			if want {
				// Operator revocation remains effective, including after a new
				// evaluator reconstructs the scope from committed evidence.
				endpoint, _ := netpolicy.Endpoint(tc.host)
				if err := r.recordEgress(t.Context(), scope, events.EventTypeNetworkEgressRevoked, map[string]any{"source": "operator command", "endpoint": endpoint}); err != nil {
					t.Fatal(err)
				}
				if d, err := evaluator.Evaluate(t.Context(), request); err != nil || d.Allowed {
					t.Fatalf("automatic grant undid revocation: %+v %v", d, err)
				}
			}
			// A recognized host also works on a distinct TCP endpoint. Community
			// still requires a separate exact grant for each port.
			request.Protocol = netpolicy.ProtocolUDP
			if d, err := evaluator.Evaluate(t.Context(), request); err == nil || d.Allowed {
				t.Fatalf("UDP allowed: %+v %v", d, err)
			}
			request.Protocol, request.Port = netpolicy.ProtocolTCP, 8443
			if d, err := evaluator.Evaluate(t.Context(), request); err != nil || d.Allowed != want {
				t.Fatalf("distinct TCP port: %+v %v; want allowed %v", d, err, want)
			}
			scope.socket = filepath.Join(t.TempDir(), "stale.sock")
			if d, err := evaluator.Evaluate(context.Background(), request); err == nil || d.Allowed {
				t.Fatalf("stale incarnation allowed: %+v %v", d, err)
			}
		})
	}
}
