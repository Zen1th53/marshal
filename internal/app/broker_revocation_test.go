package app

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/netpolicy"
	"github.com/Zen1th53/marshal/internal/permission"
)

// An in-memory transport exercises owner closure without depending on sockets.
// The separate CONNECT regression verifies a completed upstream tunnel too.
func TestCredentialOwnerAcknowledgesActiveConnection(t *testing.T) {
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
	req := permission.Request{Kind: "credential", Object: "codex"}
	if err := operator.CommandPermission(ctx, req, true, "operator grant"); err != nil {
		t.Fatal(err)
	}
	broker, err := netpolicy.NewCredentialBroker("codex", "", "synthetic-host-key")
	if err != nil {
		t.Fatal(err)
	}
	scope := &runEgress{id: "RUN-pipe-owner", provider: "codex", socket: "pipe-owner-incarnation", broker: broker, started: time.Now().UTC()}
	if err := owner.recordEgress(t.Context(), scope, events.EventTypeNetworkEgressRequested, map[string]any{"source": "run scope", "brokered": true, "allowed_endpoints": []string{"api.openai.com:443"}}); err != nil {
		t.Fatal(err)
	}
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	listener := &credentialPipeListener{brokerCloseListener: brokerCloseListener{ready: make(chan struct{}, 1), done: make(chan struct{})}, conn: server}
	proxy, err := netpolicy.NewEgressProxy(netpolicy.ProxyConfig{Broker: broker, Evaluator: &storedRunEgress{runtime: owner, scope: scope}, Listener: listener})
	if err != nil {
		t.Fatal(err)
	}
	scope.proxy = proxy
	defer proxy.Close()
	proxy.Start()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	// Hold a request in progress on the daemon-owned broker connection.
	if _, err := client.Write([]byte("CONNECT api.openai.com:443 HTTP/1.1\r\n")); err != nil {
		t.Fatal(err)
	}
	request := netpolicy.Request{Host: "api.openai.com", Port: 443, Protocol: netpolicy.ProtocolTCP}
	evaluator := &storedRunEgress{runtime: owner, scope: scope}
	if d, err := evaluator.Evaluate(t.Context(), request); err != nil || !d.Allowed {
		t.Fatalf("initial grant: %+v %v", d, err)
	}
	if err := operator.CommandPermission(ctx, req, false, "operator revoke"); err != nil {
		t.Fatal(err)
	}
	if state, err := operator.CredentialRevocationStatus(t.Context(), "codex"); err != nil || state != "pending" {
		t.Fatalf("before acknowledgement: %s %v", state, err)
	}
	if d, err := evaluator.Evaluate(t.Context(), request); err != nil || d.Allowed {
		t.Fatalf("immediate denial: %+v %v", d, err)
	}
	if err := operator.CommandPermission(ctx, req, true, "operator regrant"); err != nil {
		t.Fatal(err)
	}
	watchCtx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); owner.watchEgressRevocations(watchCtx, scope) }()
	defer func() { cancel(); <-done }()
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("exchange survived revoke")
	} else if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("exchange was not closed")
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
			t.Fatal("owner acknowledgement missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if state, err := operator.CredentialRevocationStatus(t.Context(), "claude"); err != nil || state != "none" {
		t.Fatalf("provider scope escaped: %s %v", state, err)
	}
	reopened, err := Open(t.Context(), owner.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if state, err := reopened.CredentialRevocationStatus(t.Context(), "codex"); err != nil || state != "closed" {
		t.Fatalf("acknowledgement not durable: %s %v", state, err)
	}
	if err := reopened.store.Close(); err != nil {
		t.Fatal(err)
	}
	if state, err := reopened.CredentialRevocationStatus(t.Context(), "codex"); err == nil || state == "closed" {
		t.Fatalf("unavailable history claimed closure: %s %v", state, err)
	}
}

type credentialPipeListener struct {
	brokerCloseListener
	conn net.Conn
}

func (l *credentialPipeListener) Accept() (net.Conn, error) {
	if l.conn != nil {
		c := l.conn
		l.conn = nil
		return c, nil
	}
	<-l.done
	return nil, net.ErrClosed
}
