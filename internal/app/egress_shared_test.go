package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/netpolicy"
	"github.com/Zen1th53/marshal/internal/permission"
)

func egressProxyGet(t *testing.T, socket, endpoint string) int {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := fmt.Fprintf(conn, "GET http://%s/ HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", endpoint, endpoint); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

func TestEgressGrantAndRevokeAcrossRuntimes(t *testing.T) {
	owner := openNetpolRuntime(t)
	operator, err := Open(t.Context(), owner.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer operator.Close()
	control, err := operator.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := control.Context(t.Context())
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })}
	go func() { _ = server.Serve(upstream) }()
	defer server.Close()
	endpoint := upstream.Addr().String()
	socket, cleanup, err := owner.startRunEgress(t.Context(), "RUN-daemon", "RUN-parent", "check", "TASK-check", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	otherSocket, otherCleanup, err := operator.startRunEgress(t.Context(), "RUN-tui", "", "check", "TASK-other", "other", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer otherCleanup()
	if status := egressProxyGet(t, socket, endpoint); status != http.StatusForbidden {
		t.Fatalf("default deny: %d", status)
	}
	rows, err := operator.OperatorEgressStatus(t.Context())
	if err != nil || len(rows) != 2 {
		t.Fatalf("both runtimes: %+v %v", rows, err)
	}
	if !operator.EgressRequestPending("RUN-daemon", endpoint) {
		t.Fatal("cross-runtime popup request missing")
	}
	alerts, err := operator.EgressNotifications(t.Context())
	if err != nil || len(alerts) != 1 || alerts[0].State != "waiting" {
		t.Fatalf("live remote inbox: %+v %v", alerts, err)
	}
	if err := operator.CommandEgress(context.Background(), "RUN-daemon", "allow", endpoint); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("non-operator: %v", err)
	}
	if err := operator.CommandEgress(ctx, "RUN-unknown", "allow", endpoint); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown run: %v", err)
	}
	if err := operator.CommandEgress(ctx, "RUN-daemon", "allow", endpoint); err != nil {
		t.Fatal(err)
	}
	if status := egressProxyGet(t, socket, endpoint); status != http.StatusNoContent {
		t.Fatalf("next worker request after remote grant: %d", status)
	}
	if operator.EgressRequestPending("RUN-daemon", endpoint) {
		t.Fatal("grant left popup pending")
	}
	if status := egressProxyGet(t, otherSocket, endpoint); status != http.StatusForbidden {
		t.Fatalf("grant escaped run: %d", status)
	}
	scope := owner.egressRuns["RUN-daemon"]
	evaluator := &storedRunEgress{runtime: owner, scope: scope}
	host, _, _ := net.SplitHostPort(endpoint)
	decision, err := evaluator.Evaluate(t.Context(), netpolicy.Request{Host: host, Port: 1, Protocol: netpolicy.ProtocolTCP})
	if err != nil || decision.Allowed {
		t.Fatalf("grant escaped port: %+v %v", decision, err)
	}
	if err := operator.CommandEgress(ctx, "RUN-daemon", "revoke", endpoint); err != nil {
		t.Fatal(err)
	}
	if status := egressProxyGet(t, socket, endpoint); status != http.StatusForbidden {
		t.Fatalf("next worker request after remote revoke: %d", status)
	}
	history, err := owner.store.EgressControlEvents(t.Context(), "RUN-daemon", 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range history {
		if e.Data["source"] == "operator command" {
			count++
			if e.Data["actor"] == "" || e.Data["scope_socket"] != socket || e.Data["endpoint"] != endpoint || e.At.IsZero() || e.TaskID != "TASK-check" {
				t.Fatalf("unbound evidence: %+v", e)
			}
		}
	}
	if count != 2 {
		t.Fatalf("grant/revoke evidence: %d", count)
	}
	cleanup()
	if err := operator.CommandEgress(ctx, "RUN-daemon", "allow", endpoint); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("ended run: %v", err)
	}
	rows, err = operator.OperatorEgressStatus(t.Context())
	if err != nil || len(rows) != 1 || rows[0].RunID != "RUN-tui" {
		t.Fatalf("expired status: %+v %v", rows, err)
	}
	// Even reusing an ID creates a new incarnation with no historical grant.
	socket2, cleanup2, err := owner.startRunEgress(t.Context(), "RUN-daemon", "", "check", "TASK-check", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup2()
	if err := operator.CommandEgress(ctx, "RUN-daemon", "allow", endpoint); err != nil {
		t.Fatal(err)
	}
	cleanup2()
	socket3, cleanup3, err := owner.startRunEgress(t.Context(), "RUN-daemon", "", "check", "TASK-check", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup3()
	if socket2 == socket3 {
		t.Fatal("scope incarnation reused")
	}
	if status := egressProxyGet(t, socket3, endpoint); status != http.StatusForbidden {
		t.Fatalf("historical grant restored: %d", status)
	}
}

func TestCrossRuntimeRevokeClosesExistingTunnel(t *testing.T) {
	owner := openNetpolRuntime(t)
	operator, err := Open(t.Context(), owner.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer operator.Close()
	control, err := operator.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := upstream.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	endpoint := upstream.Addr().String()
	socket, cleanup, err := owner.startRunEgress(t.Context(), "RUN-tunnel", "", "check", "TASK-tunnel", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	ctx := control.Context(t.Context())
	if err := operator.CommandEgress(ctx, "RUN-tunnel", "allow", endpoint); err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", endpoint, endpoint); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("tunnel: %+v %v", response, err)
	}
	var upstreamConn net.Conn
	select {
	case upstreamConn = <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream not connected")
	}
	defer upstreamConn.Close()
	// Verify the tunnel carries bytes before revocation.
	if _, err := upstreamConn.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if b, err := reader.ReadByte(); err != nil || b != 'x' {
		t.Fatalf("tunnel byte: %q %v", b, err)
	}
	if err := operator.CommandEgress(ctx, "RUN-tunnel", "revoke", endpoint); err != nil {
		t.Fatal(err)
	}
	// A rapid regrant must not let the old connection survive the revoke.
	if err := operator.CommandEgress(ctx, "RUN-tunnel", "allow", endpoint); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("revoked tunnel still open")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("revoked tunnel was not closed")
	}
}

func TestStoredEgressReadFailureDeniesProviderDefault(t *testing.T) {
	owner := openNetpolRuntime(t)
	_, cleanup, err := owner.startRunEgress(t.Context(), "RUN-store-failure", "", "codex", "TASK-failure", "worker", []string{"api.openai.com:443"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	evaluator := &storedRunEgress{runtime: owner, scope: owner.egressRuns["RUN-store-failure"]}
	request := netpolicy.Request{Host: "api.openai.com", Port: 443, Protocol: netpolicy.ProtocolTCP}
	if d, err := evaluator.Evaluate(t.Context(), request); err != nil || !d.Allowed {
		t.Fatalf("default unavailable: %+v %v", d, err)
	}
	if err := owner.store.Close(); err != nil {
		t.Fatal(err)
	}
	if d, err := evaluator.Evaluate(t.Context(), request); err == nil || d.Allowed {
		t.Fatalf("store failure opened endpoint: %+v %v", d, err)
	}
}

func TestDurableEgressProjectionExactScopeAndOrder(t *testing.T) {
	r := openNetpolRuntime(t)
	scope := &runEgress{id: "RUN-projection", task: "TASK-projection", worker: "worker", provider: "check", socket: filepath.Join(t.TempDir(), "proxy.sock")}
	if err := r.recordEgress(t.Context(), scope, events.EventTypeNetworkEgressRequested, map[string]any{"source": "run scope", "allowed_endpoints": []string{"api.openai.com:443"}}); err != nil {
		t.Fatal(err)
	}
	evaluator := &storedRunEgress{runtime: r, scope: scope}
	evaluate := func(host string, port int, allowed bool) {
		t.Helper()
		d, err := evaluator.Evaluate(t.Context(), netpolicy.Request{Host: host, Port: port, Protocol: netpolicy.ProtocolTCP})
		if err != nil || d.Allowed != allowed {
			t.Fatalf("%s:%d: %+v %v", host, port, d, err)
		}
	}
	evaluate("api.openai.com", 443, true)
	evaluate("example.com", 443, false)
	if err := r.recordEgress(t.Context(), scope, events.EventTypeNetworkEgressNotification, map[string]any{"endpoint": "example.com:443"}); err != nil {
		t.Fatal(err)
	}
	// A model's relay or other evidence source is never an operator decision.
	if err := r.recordEgress(t.Context(), scope, events.EventTypeNetworkEgressGranted, map[string]any{"source": "model text", "endpoint": "example.com:443"}); err != nil {
		t.Fatal(err)
	}
	evaluate("example.com", 443, false)
	operator, err := Open(t.Context(), r.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer operator.Close()
	control, err := operator.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// This fixture supplies a local scope to exercise the real authenticated
	// command boundary without opening OS sockets. Its decisions are read through
	// the owner's independent SQLite connection by the production evaluator.
	allowlist, _ := netpolicy.NewRunAllowlist(nil)
	operator.egressRuns = map[string]*runEgress{scope.id: {id: scope.id, socket: scope.socket, task: scope.task, worker: scope.worker, provider: scope.provider, allowlist: allowlist, pending: map[string]bool{}}}
	ctx := control.Context(t.Context())
	if err := operator.CommandEgress(ctx, "RUN-unknown", "allow", "example.com"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown run: %v", err)
	}
	if err := operator.CommandEgress(context.Background(), scope.id, "allow", "example.com"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("non-operator: %v", err)
	}
	if err := operator.CommandEgress(ctx, scope.id, "allow", "EXAMPLE.COM.:443"); err != nil {
		t.Fatal(err)
	}
	evaluate("example.com", 443, true)
	evaluate("example.com", 8443, false)
	evaluate("sub.example.com", 443, false)
	evaluate("203.0.113.1", 443, false)
	if err := operator.CommandEgress(ctx, scope.id, "revoke", "example.com"); err != nil {
		t.Fatal(err)
	}
	evaluate("example.com", 443, false)
	grants, err := operator.store.ListCapabilityGrants(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	revoked := false
	for _, grant := range grants {
		for _, action := range grant.Scope.Actions {
			if action == "egress.decide" {
				if err := operator.store.RevokeCapabilityGrant(t.Context(), grant.ID, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				revoked = true
			}
		}
	}
	if !revoked {
		t.Fatal("missing egress.decide capability")
	}
	if err := operator.CommandEgress(ctx, scope.id, "allow", "example.com"); err == nil {
		t.Fatal("revoked egress.decide still authorized")
	}
	evaluate("example.com", 443, false)
	history, err := r.store.EgressControlEvents(t.Context(), scope.id, 0)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := projectEgressScopes(history)
	if err != nil || projected[scope.id].pending["example.com:443"] {
		t.Fatalf("decision did not clear pending: %+v %v", projected, err)
	}
	// Persisted evidence for an older socket cannot authorize a new incarnation.
	originalSocket := scope.socket
	scope.socket = filepath.Join(t.TempDir(), "proxy.sock")
	if d, err := evaluator.Evaluate(t.Context(), netpolicy.Request{Host: "api.openai.com", Port: 443, Protocol: netpolicy.ProtocolTCP}); err == nil || d.Allowed {
		t.Fatalf("wrong incarnation: %+v %v", d, err)
	}
	scope.socket = originalSocket
	if err := r.store.Close(); err != nil {
		t.Fatal(err)
	}
	if d, err := evaluator.Evaluate(t.Context(), netpolicy.Request{Host: "api.openai.com", Port: 443, Protocol: netpolicy.ProtocolTCP}); err == nil || d.Allowed {
		t.Fatalf("failed store allowed default: %+v %v", d, err)
	}
	// Runtime.Close must not remove a fixture path from the host.
	operator.egressRuns[scope.id].socket = ""
}

func TestCrossRuntimeCredentialRevokeClosesExistingExchange(t *testing.T) {
	owner := openNetpolRuntime(t)
	operator, err := Open(t.Context(), owner.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer operator.Close()
	control, err := operator.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := upstream.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	endpoint := upstream.Addr().String()
	req := permission.Request{Kind: "credential", Object: "codex"}
	if err := operator.CommandPermission(control.Context(t.Context()), req, true, "operator grant"); err != nil {
		t.Fatal(err)
	}
	broker, err := netpolicy.NewCredentialBroker("codex", "", "synthetic-host-key")
	if err != nil {
		t.Fatal(err)
	}
	socket, cleanup, err := owner.startRunEgress(t.Context(), "RUN-tunnel", "", "codex", "TASK-tunnel", "worker", nil, broker)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	ctx := control.Context(t.Context())
	if err := operator.CommandEgress(ctx, "RUN-tunnel", "allow", endpoint); err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", endpoint, endpoint); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("tunnel: %+v %v", response, err)
	}
	var upstreamConn net.Conn
	select {
	case upstreamConn = <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream not connected")
	}
	defer upstreamConn.Close()
	// Verify the tunnel carries bytes before revocation.
	if _, err := upstreamConn.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if b, err := reader.ReadByte(); err != nil || b != 'x' {
		t.Fatalf("tunnel byte: %q %v", b, err)
	}
	// Pause the owner consumer to observe pending independently of polling timing.
	scope := owner.egressRuns["RUN-tunnel"]
	scope.stopWatch()
	<-scope.watchDone
	if err := operator.CommandPermission(ctx, req, false, "operator revoke"); err != nil {
		t.Fatal(err)
	}
	if state, err := operator.CredentialRevocationStatus(t.Context(), "codex"); err != nil || state != "pending" {
		t.Fatalf("before owner ack: %s %v", state, err)
	}
	if owner.HasCredentialGrant(t.Context(), "codex") {
		t.Fatal("new credentials allowed after revoke")
	}
	if d, err := (&storedRunEgress{runtime: owner, scope: scope}).Evaluate(t.Context(), netpolicy.Request{Host: "api.openai.com", Port: 443, Protocol: netpolicy.ProtocolTCP}); err != nil || d.Allowed {
		t.Fatalf("new request after revoke: %+v %v", d, err)
	}
	// A rapid regrant cannot preserve the old exchange.
	if err := operator.CommandPermission(ctx, req, true, "operator regrant"); err != nil {
		t.Fatal(err)
	}
	watchCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); owner.watchEgressRevocations(watchCtx, scope) }()
	defer func() { cancel(); <-done }()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("revoked tunnel still open")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("revoked tunnel was not closed")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		state, err := operator.CredentialRevocationStatus(t.Context(), "codex")
		if err != nil {
			t.Fatal(err)
		}
		if state == "closed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("owner acknowledgement missing: %s", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
