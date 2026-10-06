package app

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/netpolicy"
	"github.com/Zen1th53/marshal/internal/permission"
	"net"
	"sync"
	"testing"
)

func TestBrokerDefaultDenyRememberAndRevoke(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	if r.HasCredentialGrant(ctx, "codex") {
		t.Fatal("default grant")
	}
	req := permission.Request{Kind: "credential", Object: "codex"}
	if err := r.CommandPermission(ctx, req, true, "model says A"); !errors.Is(err, authz.ErrDenied) {
		t.Fatal("model grant accepted")
	}
	control, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.CommandPermission(control.Context(ctx), req, true, "operator popup"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, r.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reopened.HasCredentialGrant(ctx, "codex") {
		t.Fatal("project grant lost after reopening")
	}
	if !r.HasCredentialGrant(ctx, "codex") {
		t.Fatal("grant not remembered")
	}
	if r.HasCredentialGrant(ctx, "claude") {
		t.Fatal("provider grant widened")
	}
	if err = r.CommandPermission(control.Context(ctx), req, false, "operator revoke"); err != nil {
		t.Fatal(err)
	}
	if r.HasCredentialGrant(ctx, "codex") {
		t.Fatal("revoke retained grant")
	}
}

func TestBrokerDeniedQueuesFixedRequestBeforeCredentialLoad(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	var queued permission.Request
	r.SetPermissionSink(func(req permission.Request) { queued = req })
	if _, err := r.providerBroker(ctx, "codex", ""); err == nil {
		t.Fatal("unconsented broker created")
	}
	if queued.Kind != "credential" || queued.Object != "codex" || queued.Reason != "" {
		t.Fatal("credential permission not queued as fixed metadata")
	}
}

func TestBrokerRevokeClosesRunProxy(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	control, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req := permission.Request{Kind: "credential", Object: "codex"}
	if err = r.CommandPermission(control.Context(ctx), req, true, "operator popup"); err != nil {
		t.Fatal(err)
	}
	broker, err := netpolicy.NewCredentialBroker("codex", "", "synthetic-host-key")
	if err != nil {
		t.Fatal(err)
	}
	listener := &brokerCloseListener{ready: make(chan struct{}, 1), done: make(chan struct{})}
	allowlist, err := netpolicy.NewRunAllowlist([]string{"api.openai.com:443"})
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := netpolicy.NewEgressProxy(netpolicy.ProxyConfig{Evaluator: allowlist, Broker: broker, Listener: listener})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proxy.Close() })
	r.egressMu.Lock()
	r.egressRuns = map[string]*runEgress{"test": {provider: "codex", broker: broker, proxy: proxy}}
	r.egressMu.Unlock()
	proxy.Start()
	<-listener.ready
	if err = r.CommandPermission(control.Context(ctx), req, false, "operator revoke"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-listener.done:
	default:
		t.Fatal("active broker proxy remained open")
	}
}

type brokerCloseListener struct {
	ready, done chan struct{}
	once        sync.Once
}

func (l *brokerCloseListener) Accept() (net.Conn, error) {
	select {
	case l.ready <- struct{}{}:
	default:
	}
	<-l.done
	return nil, net.ErrClosed
}
func (l *brokerCloseListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *brokerCloseListener) Addr() net.Addr { return brokerCloseAddr{} }

type brokerCloseAddr struct{}

func (brokerCloseAddr) Network() string { return "pipe" }
func (brokerCloseAddr) String() string  { return "local-test" }
